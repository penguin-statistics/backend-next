package shims

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
)

type ResultController struct {
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	AccountService       *service.AccountService
	ItemService          *service.ItemService
	StageService         *service.StageService
}

func RegisterResultController(
	v2 *server.V2,
	dropMatrixService *service.DropMatrixService,
	patternMatrixService *service.PatternMatrixService,
	trendService *service.TrendService,
	accountService *service.AccountService,
	itemService *service.ItemService,
	stageService *service.StageService,
) {
	c := &ResultController{
		DropMatrixService:    dropMatrixService,
		PatternMatrixService: patternMatrixService,
		TrendService:         trendService,
		AccountService:       accountService,
		ItemService:          itemService,
		StageService:         stageService,
	}

	v2.Get("/result/matrix", c.GetDropMatrix)
	v2.Get("/result/pattern", c.GetPatternMatrix)
	v2.Get("/result/trends", c.GetTrends)
	v2.Post("/result/advanced", c.AdvancedQuery)
}

// @Summary      Get DropMatrix
// @Tags         Result
// @Produce      json
// @Param        server            query string "CN"  "Server"
// @Param        is_personal       query bool   false "Whether to query for personal drop matrix or not"
// @Param        show_closed_zones query    bool   false "Whether to show closed stages or not"
// @Param        stageFilter       query    string ""    "Comma separated list of ark stage ids"
// @Param        itemFilter        query    string ""    "Comma separated list of ark item ids"
// @Success      200               {object} shims.DropMatrixQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/result/matrix [GET]
// @Deprecated
func (c *ResultController) GetDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
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

	shimQueryResult, err := c.DropMatrixService.GetShimMaxAccumulableDropMatrixResults(ctx.Context(), server, showClosedZones, stageFilterStr, itemFilterStr, &accountId)
	if err != nil {
		return err
	}

	useCache := !accountId.Valid && stageFilterStr == "" && itemFilterStr == ""
	if useCache {
		key := server + constants.RedisSeparator + strconv.FormatBool(showClosedZones)
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimMaxAccumulableDropMatrixResults#server|showClosedZoned:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		ctx.Response().Header.SetLastModified(lastModifiedTime)
	}

	return ctx.JSON(shimQueryResult)
}

// @Summary      Get PatternMatrix
// @Tags         Result
// @Produce      json
// @Param        server            query string "CN"  "Server"
// @Param        is_personal       query bool   false "Whether to query for personal pattern matrix or not"
// @Success      200               {object} shims.PatternMatrixQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/result/pattern [GET]
// @Deprecated
func (c *ResultController) GetPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
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

	shimResult, err := c.PatternMatrixService.GetShimLatestPatternMatrixResults(ctx.Context(), server, &accountId)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimLatestPatternMatrixResults#server:"+server+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		ctx.Response().Header.SetLastModified(lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

// @Summary      Get Trends
// @Tags         Result
// @Produce      json
// @Param        server            query string "CN"  "Server"
// @Success      200               {object} shims.TrendQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/result/trends [GET]
// @Deprecated
func (c *ResultController) GetTrends(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	shimResult, err := c.TrendService.GetShimSavedTrendResults(ctx.Context(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimSavedTrendResults#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	ctx.Response().Header.SetLastModified(lastModifiedTime)

	return ctx.JSON(shimResult)
}

// @Summary      Execute Advanced Query
// @Tags         Result
// @Produce      json
// @Success      200     {object}  models.AdvancedQueryReques
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/advanced [POST]
func (c *ResultController) AdvancedQuery(ctx *fiber.Ctx) error {
	var request types.AdvancedQueryRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	result := &shims.AdvancedQueryResult{
		AdvancedResults: make([]interface{}, 0),
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

func (c *ResultController) handleAdvancedQuery(ctx *fiber.Ctx, query *types.AdvancedQuery) (interface{}, error) {
	// handle isPersonal (might be null) and account
	isPersonal := false
	if query.IsPersonal != nil && query.IsPersonal.Valid {
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
	startTime_milli := constants.ServerStartTimeMapMillis[query.Server]
	if query.StartTime != nil && query.StartTime.Valid {
		startTime_milli = query.StartTime.Int64
	}
	startTime := time.UnixMilli(startTime_milli)

	// handle end time (might be null)
	endTime_milli := constants.FakeEndTimeMilli
	if query.EndTime != nil && query.EndTime.Valid {
		endTime_milli = query.EndTime.Int64
	}
	endTime := time.UnixMilli(endTime_milli)

	// handle ark stage id
	stage, err := c.StageService.GetStageByArkId(ctx.Context(), query.StageID)
	if err != nil {
		return nil, err
	}

	// handle item ids
	itemIds := make([]int, 0)
	for _, arkItemID := range query.ItemIDs {
		item, err := c.ItemService.GetItemByArkId(ctx.Context(), arkItemID)
		if err != nil {
			return nil, err
		}
		itemIds = append(itemIds, item.ItemID)
	}

	// if there is no interval, then do drop matrix query, otherwise do trend query
	if query.Interval == nil || !query.Interval.Valid {
		timeRange := &models.TimeRange{
			StartTime: &startTime,
			EndTime:   &endTime,
		}
		return c.DropMatrixService.GetShimCustomizedDropMatrixResults(ctx.Context(), query.Server, timeRange, []int{stage.StageID}, itemIds, &accountId)
	} else {
		// interval originally is in milliseconds, so we need to convert it to nanoseconds
		intervalLength := time.Duration(query.Interval.Int64 * 1e7).Round(time.Hour)
		if intervalLength.Hours() < 1 {
			return nil, errors.New("interval length must be greater than 1 hour")
		}
		intervalNum := c.calcIntervalNum(startTime, endTime, intervalLength)
		if intervalNum > constants.MaxIntervalNum {
			return nil, fmt.Errorf("interval length is too long, max interval length is %d sections", constants.MaxIntervalNum)
		}

		shimTrendQueryResult, err := c.TrendService.GetShimCustomizedTrendResults(ctx.Context(), query.Server, &startTime, intervalLength, intervalNum, []int{stage.StageID}, itemIds, &accountId)
		if err != nil {
			return nil, err
		}
		return shimTrendQueryResult, nil
	}
}

func (c *ResultController) calcIntervalNum(startTime, endTime time.Time, intervalLength time.Duration) int {
	diff := endTime.Sub(startTime)
	// implicit float64 to int: drops fractional part (truncates towards 0)
	return int(int(diff.Hours()) / int(intervalLength.Hours()))
}
