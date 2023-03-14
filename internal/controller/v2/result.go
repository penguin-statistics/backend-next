package v2

import (
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	cachemiddleware "github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

// ErrIntervalLengthTooSmall is returned when the interval length is invalid
var ErrIntervalLengthTooSmall = pgerr.ErrInvalidReq.Msg("interval length must be greater than 1 hour")

type Result struct {
	fx.In

	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	AccountService       *service.Account
	ItemService          *service.Item
	StageService         *service.Stage
}

func RegisterResult(v2 *svr.V2, c Result) {
	group := v2.Group("/result")

	// Cache requests with itemFilter and stageFilter as there appears to be an unknown source requesting
	// with such behaviors very eagerly, causing a relatively high load on the database.
	log.Info().Msg("enabling fiber-level cache & limiter for requests under /result group which contain itemFilter or stageFilter query params.")

	group.Use(limiter.New(limiter.Config{
		Next: func(c *fiber.Ctx) bool {
			if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
				return false
			}
			return true
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    "TOO_MANY_REQUESTS",
				"message": "Your client is sending requests too frequently. The Penguin Stats result matrix are updated periodically and should not be requested too frequently.",
			})
		},
		Max:        300,
		Expiration: time.Minute * 5,
	}))

	group.Use(cachemiddleware.New(cachemiddleware.Config{
		Next: func(c *fiber.Ctx) bool {
			// only cache requests with itemFilter and stageFilter query params
			if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
				return false
			}
			return true
		},
		CacheHeader:  constant.CacheHeader,
		CacheControl: true,
		Expiration:   time.Minute * 5,
		KeyGenerator: func(c *fiber.Ctx) string {
			return utils.CopyString(c.OriginalURL())
		},
	}))

	group.Get("/matrix", middlewares.ValidateServerAsQuery, c.GetDropMatrix)
	group.Get("/pattern", middlewares.ValidateServerAsQuery, c.GetPatternMatrix)
	group.Get("/trends", middlewares.ValidateServerAsQuery, c.GetTrends)
	group.Post("/advanced", limiter.New(limiter.Config{
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    "TOO_MANY_REQUESTS",
				"message": "Your client is sending requests too frequently. The Penguin Stats advanced query API is limited to 30 requests per 5 minutes.",
			})
		},
		Max:        30,
		Expiration: time.Minute * 5,
	}), c.AdvancedQuery)
}

// @Summary   Get Drop Matrix
// @Tags      Result
// @Produce   json
// @Param     server             query     string                         true   "Server; default to CN"  Enums(CN, US, JP, KR)
// @Param     is_personal        query     bool                           false  "Whether to query for personal drop matrix or not. If `is_personal` equals to `true`, a valid PenguinID would be required to be provided (PenguinIDAuth)"
// @Param     show_closed_zones  query     bool                           false  "Whether to show closed stages or not"
// @Param     stageFilter        query     []string                       false  "Comma separated list of stage IDs to filter"  collectionFormat(csv)
// @Param     itemFilter         query     []string                       false  "Comma separated list of item IDs to filter"   collectionFormat(csv)
// @Success   200                {object}  modelv2.DropMatrixQueryResult  "Drop Matrix response"
// @Failure   500                {object}  pgerr.PenguinError             "An unexpected error occurred"
// @Security  PenguinIDAuth
// @Router    /PenguinStats/api/v2/result/matrix [GET]
func (c *Result) GetDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}
	showClosedZones, err := strconv.ParseBool(ctx.Query("show_closed_zones", "false"))
	if err != nil {
		return err
	}
	stageFilterStr := ctx.Query("stageFilter")
	itemFilterStr := ctx.Query("itemFilter")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimQueryResult, err := c.DropMatrixService.GetShimDropMatrix(ctx.UserContext(), server, showClosedZones, stageFilterStr, itemFilterStr, accountId, constant.SourceCategoryAll)
	if err != nil {
		return err
	}

	useCache := !accountId.Valid && stageFilterStr == "" && itemFilterStr == ""
	if useCache {
		key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + constant.SourceCategoryAll
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalDropMatrix#server|showClosedZoned|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimQueryResult)
}

// @Summary   Get Pattern Matrix
// @Tags      Result
// @Produce   json
// @Param     server       query     string  true   "Server; default to CN"  Enums(CN, US, JP, KR)
// @Param     is_personal  query     bool    false  "Whether to query for personal drop matrix or not. If `is_personal` equals to `true`, a valid PenguinID would be required to be provided (PenguinIDAuth)"
// @Success   200          {object}  modelv2.PatternMatrixQueryResult
// @Failure   500          {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Security  PenguinIDAuth
// @Router    /PenguinStats/api/v2/result/pattern [GET]
func (c *Result) GetPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimResult, err := c.PatternMatrixService.GetShimLatestPatternMatrixResults(ctx.UserContext(), server, accountId, constant.SourceCategoryAll)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		key := server + constant.CacheSep + constant.SourceCategoryAll
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimLatestPatternMatrixResults#server|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

// @Summary  Get Trends
// @Tags     Result
// @Produce  json
// @Param    server  query     string  true  "Server; default to CN"  Enums(CN, US, JP, KR)
// @Success  200     {object}  modelv2.TrendQueryResult
// @Failure  500     {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/result/trends [GET]
func (c *Result) GetTrends(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	shimResult, err := c.TrendService.GetShimSavedTrendResults(ctx.UserContext(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimSavedTrendResults#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)

	return ctx.JSON(shimResult)
}

// @Summary  Execute Advanced Query
// @Tags     Result
// @Produce  json
// @Param    query  body      types.AdvancedQueryRequest                                                     true  "Query"
// @Success  200    {object}  modelv2.AdvancedQueryResult{advanced_results=[]modelv2.DropMatrixQueryResult}  "Drop Matrix Response: when `interval` has been left undefined."
// @Success  202    {object}  modelv2.AdvancedQueryResult{advanced_results=[]modelv2.TrendQueryResult}       "Trend Response: when `interval` has been defined a value greater than `0`. Notice that this response still responds with a status code of `200`, but due to swagger limitations, to denote a different response with the same status code is not possible. Therefore, a status code of `202` is used, only for the purpose of workaround."
// @Failure  500    {object}  pgerr.PenguinError                                                             "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/advanced [POST]
func (c *Result) AdvancedQuery(ctx *fiber.Ctx) error {
	var request types.AdvancedQueryRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	result := &modelv2.AdvancedQueryResult{
		AdvancedResults: make([]any, 0),
	}
	for _, query := range request.Queries {
		oneResult, err := c.handleAdvancedQuery(ctx, query)
		if err != nil {
			return err
		}
		result.AdvancedResults = append(result.AdvancedResults, oneResult)
	}
	return ctx.JSON(result)
}

func (c *Result) handleAdvancedQuery(ctx *fiber.Ctx, query *types.AdvancedQuery) (any, error) {
	// handle isPersonal (might be null) and account
	isPersonal := false
	if query.IsPersonal.Valid {
		isPersonal = query.IsPersonal.Bool
	}
	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return nil, err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	// handle start time (might be null)
	startTimeMilli := constant.ServerStartTimeMapMillis[query.Server]
	if query.StartTime.Valid {
		startTimeMilli = query.StartTime.Int64
	}
	startTime := time.UnixMilli(startTimeMilli)

	// handle end time (might be null)
	endTimeMilli := time.Now().UnixMilli()
	if query.EndTime.Valid {
		endTimeMilli = query.EndTime.Int64
	}
	endTime := time.UnixMilli(endTimeMilli)

	// handle ark stage id
	stage, err := c.StageService.GetStageByArkId(ctx.UserContext(), query.StageID)
	if err != nil {
		return nil, err
	}

	// handle item ids
	itemIds := make([]int, 0)
	for _, arkItemID := range query.ItemIDs {
		item, err := c.ItemService.GetItemByArkId(ctx.UserContext(), arkItemID)
		if err != nil {
			return nil, err
		}
		itemIds = append(itemIds, item.ItemID)
	}

	// handle sourceCategory, default to all
	sourceCategory := query.SourceCategory
	if sourceCategory == "" {
		sourceCategory = constant.SourceCategoryAll
	}

	// if there is no interval, then do drop matrix query, otherwise do trend query
	if !query.Interval.Valid {
		timeRange := &model.TimeRange{
			StartTime: &startTime,
			EndTime:   &endTime,
		}
		return c.DropMatrixService.GetShimCustomizedDropMatrixResults(ctx.UserContext(), query.Server, timeRange, []int{stage.StageID}, itemIds, accountId, sourceCategory)
	} else {
		// interval originally is in milliseconds, so we need to convert it to nanoseconds
		intervalLength := time.Duration(query.Interval.Int64 * 1e6).Round(time.Hour)
		if intervalLength.Hours() < 1 {
			return nil, ErrIntervalLengthTooSmall
		}
		intervalNum := c.calcIntervalNum(startTime, endTime, intervalLength)
		if intervalNum > constant.MaxIntervalNum {
			return nil, pgerr.ErrInvalidReq.Msg("too many sections: interval number is %d sections, which is larger than %d sections", intervalNum, constant.MaxIntervalNum)
		}

		shimTrendQueryResult, err := c.TrendService.GetShimCustomizedTrendResults(ctx.UserContext(), query.Server, &startTime, intervalLength, intervalNum, []int{stage.StageID}, itemIds, accountId, sourceCategory)
		if err != nil {
			return nil, err
		}
		return shimTrendQueryResult, nil
	}
}

func (c *Result) calcIntervalNum(startTime, endTime time.Time, intervalLength time.Duration) int {
	diff := endTime.Sub(startTime)
	// implicit float64 to int: drops fractional part (truncates towards 0)
	return int(diff.Hours()) / int(intervalLength.Hours())
}
