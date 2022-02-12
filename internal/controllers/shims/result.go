package shims

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
	"github.com/penguin-statistics/backend-next/internal/utils/shimutils"
)

type ResultController struct {
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	AccountService       *service.AccountService
	ItemService          *service.ItemService
	StageService         *service.StageService
	ShimUtil             *shimutils.ShimUtil
}

func RegisterResultController(
	v2 *server.V2,
	dropMatrixService *service.DropMatrixService,
	patternMatrixService *service.PatternMatrixService,
	trendService *service.TrendService,
	accountService *service.AccountService,
	itemService *service.ItemService,
	stageService *service.StageService,
	shimUtil *shimutils.ShimUtil,
) {
	c := &ResultController{
		DropMatrixService:    dropMatrixService,
		PatternMatrixService: patternMatrixService,
		TrendService:         trendService,
		AccountService:       accountService,
		ItemService:          itemService,
		StageService:         stageService,
		ShimUtil:             shimUtil,
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
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Query("server", "CN")
	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}
	showClosedZones, err := strconv.ParseBool(ctx.Query("show_closed_zones", "false"))
	if err != nil {
		return err
	}
	stageFilterStr := ctx.Query("stageFilter", "")
	itemFilterStr := ctx.Query("itemFilter", "")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		if account == nil {
			return fmt.Errorf("account not found")
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimQueryResult, err := c.DropMatrixService.GetShimMaxAccumulableDropMatrixResults(ctx, server, showClosedZones, stageFilterStr, itemFilterStr, &accountId)
	if err != nil {
		return err
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
	// TODO: the whole result should be cached, and populated when server starts
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
		if account == nil {
			return fmt.Errorf("account not found")
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.PatternMatrixService.GetSavedPatternMatrixResults(ctx, server, &accountId)
	if err != nil {
		return err
	}
	shimResult, err := c.ShimUtil.ApplyShimForPatternMatrixQuery(ctx, queryResult)
	if err != nil {
		return err
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
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Query("server", "CN")

	shimResult, err := c.TrendService.GetShimSavedTrendResults(ctx, server)
	if err != nil {
		return err
	}
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
		if account == nil {
			return nil, fmt.Errorf("account not found")
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	// handle start time (might be null)
	startTime_milli := constants.SERVER_START_TIME_MAP_MILLI[query.Server]
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
	stage, err := c.StageService.GetStageByArkId(ctx, query.StageID)
	if err != nil {
		return nil, err
	}

	// handle item ids
	itemIds := make([]int, 0)
	for _, arkItemID := range query.ItemIDs {
		item, err := c.ItemService.GetItemByArkId(ctx, arkItemID)
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
		return c.DropMatrixService.GetShimCustomizedDropMatrixResults(ctx, query.Server, timeRange, []int{stage.StageID}, itemIds, &accountId)
	} else {
		intervalLength_hrs := int(query.Interval.Int64 / (1000 * 60 * 60))
		if intervalLength_hrs == 0 {
			return nil, fmt.Errorf("interval length must be greater than 1 hour")
		}
		intervalNum := c.calcIntervalNum(startTime, endTime, intervalLength_hrs)
		if intervalNum > constants.MaxIntervalNum {
			return nil, fmt.Errorf("intervalNum too large")
		}

		shimTrendQueryResult, err := c.TrendService.GetShimCustomizedTrendResults(ctx, query.Server, &startTime, intervalLength_hrs, intervalNum, []int{stage.StageID}, itemIds, &accountId)
		if err != nil {
			return nil, err
		}
		return shimTrendQueryResult, nil
	}
}

func (c *ResultController) calcIntervalNum(startTime, endTime time.Time, intervalLength_hrs int) int {
	diff := endTime.Unix() - startTime.Unix()
	return int(diff / (3600 * int64(intervalLength_hrs)))
}
