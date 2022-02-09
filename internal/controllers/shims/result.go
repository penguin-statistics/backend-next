package shims

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
)

type ResultController struct {
	DropMatrixService         *service.DropMatrixService
	PatternMatrixService      *service.PatternMatrixService
	TrendService              *service.TrendService
	AccountService            *service.AccountService
	DropInfoService           *service.DropInfoService
	ItemService               *service.ItemService
	StageService              *service.StageService
	DropPatternElementService *service.DropPatternElementService
}

func RegisterResultController(
	v2 *server.V2,
	dropMatrixService *service.DropMatrixService,
	patternMatrixService *service.PatternMatrixService,
	trendService *service.TrendService,
	accountService *service.AccountService,
	dropInfoService *service.DropInfoService,
	itemService *service.ItemService,
	stageService *service.StageService,
	dropPatternElementService *service.DropPatternElementService,
) {
	c := &ResultController{
		DropMatrixService:         dropMatrixService,
		PatternMatrixService:      patternMatrixService,
		TrendService:              trendService,
		AccountService:            accountService,
		DropInfoService:           dropInfoService,
		ItemService:               itemService,
		StageService:              stageService,
		DropPatternElementService: dropPatternElementService,
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
		account, err := c.AccountService.GetAccountFromAuthHeader(ctx, ctx.Get("Authorization"))
		if err != nil {
			return err
		}
		if account == nil {
			return fmt.Errorf("account not found")
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.DropMatrixService.GetSavedDropMatrixResults(ctx, server, &accountId)
	if err != nil {
		return err
	}
	shimResult, err := c.applyShimForDropMatrixQuery(ctx, server, showClosedZones, stageFilterStr, itemFilterStr, queryResult)
	if err != nil {
		return err
	}
	return ctx.JSON(shimResult)
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
		account, err := c.AccountService.GetAccountFromAuthHeader(ctx, ctx.Get("Authorization"))
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
	shimResult, err := c.applyShimForPatternMatrixQuery(ctx, queryResult)
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

	queryResult, err := c.TrendService.GetSavedTrendResults(ctx, server)
	if err != nil {
		return err
	}
	shimResult, err := c.applyShimForTrendQuery(ctx, queryResult)
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

func (c *ResultController) applyShimForDropMatrixQuery(ctx *fiber.Ctx, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, queryResult *models.DropMatrixQueryResult) (*shims.DropMatrixQueryResult, error) {
	// get opening stages from dropinfos
	var openingStageIds []int
	if !showClosedZones {
		currentDropInfos, err := c.DropInfoService.GetCurrentDropInfosByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		linq.From(currentDropInfos).SelectT(func(el *models.DropInfo) int { return el.StageID }).Distinct().ToSlice(&openingStageIds)
	}

	// convert comma-splitted stage filter param to a hashset
	stageFilter := make([]string, 0)
	if stageFilterStr != "" {
		stageFilter = strings.Split(stageFilterStr, ",")
	}
	stageFilterSet := make(map[string]struct{}, len(stageFilter))
	for _, stageIdStr := range stageFilter {
		stageFilterSet[stageIdStr] = struct{}{}
	}

	// convert comma-splitted item filter param to a hashset
	itemFilter := make([]string, 0)
	if itemFilterStr != "" {
		itemFilter = strings.Split(itemFilterStr, ",")
	}
	itemFilterSet := make(map[string]struct{}, len(itemFilter))
	for _, itemIdStr := range itemFilter {
		itemFilterSet[itemIdStr] = struct{}{}
	}

	results := &shims.DropMatrixQueryResult{
		Matrix: make([]*shims.OneDropMatrixElement, 0),
	}
	for _, el := range queryResult.Matrix {
		if !showClosedZones && !linq.From(openingStageIds).Contains(el.StageID) {
			continue
		}

		stage, err := c.StageService.GetStageById(ctx, el.StageID)
		if err != nil {
			return nil, err
		}
		if len(stageFilterSet) > 0 {
			if _, ok := stageFilterSet[stage.ArkStageID]; !ok {
				continue
			}
		}

		item, err := c.ItemService.GetItemById(ctx, el.ItemID)
		if err != nil {
			return nil, err
		}
		if len(itemFilterSet) > 0 {
			if _, ok := itemFilterSet[item.ArkItemID]; !ok {
				continue
			}
		}

		endTime := null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		oneDropMatrixElement := shims.OneDropMatrixElement{
			StageID:   stage.ArkStageID,
			ItemID:    item.ArkItemID,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   &endTime,
		}
		if oneDropMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
			oneDropMatrixElement.EndTime = nil
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}

func (c *ResultController) applyShimForPatternMatrixQuery(ctx *fiber.Ctx, queryResult *models.DropPatternQueryResult) (*shims.PatternMatrixQueryResult, error) {
	results := &shims.PatternMatrixQueryResult{
		PatternMatrix: make([]*shims.OnePatternMatrixElement, 0),
	}
	var groupedResults []linq.Group
	linq.From(queryResult.DropPatterns).
		GroupByT(
			func(el *models.OneDropPattern) int { return el.PatternID },
			func(el *models.OneDropPattern) *models.OneDropPattern { return el },
		).ToSlice(&groupedResults)
	for _, group := range groupedResults {
		patternId := group.Key.(int)
		for _, el := range group.Group {
			oneDropPattern := el.(*models.OneDropPattern)
			stage, err := c.StageService.GetStageById(ctx, oneDropPattern.StageID)
			if err != nil {
				return nil, err
			}
			endTime := null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
			dropPatternElements, err := c.DropPatternElementService.GetDropPatternElementsByPatternId(ctx, patternId)
			if err != nil {
				return nil, err
			}
			// create pattern object from dropPatternElements
			pattern := shims.Pattern{
				Drops: make([]*shims.OneDrop, 0),
			}
			for _, dropPatternElement := range dropPatternElements {
				item, err := c.ItemService.GetItemById(ctx, dropPatternElement.ItemID)
				if err != nil {
					return nil, err
				}
				pattern.Drops = append(pattern.Drops, &shims.OneDrop{
					ItemID:   item.ArkItemID,
					Quantity: dropPatternElement.Quantity,
				})
			}
			onePatternMatrixElement := shims.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   &endTime,
				Pattern:   &pattern,
			}
			if onePatternMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
				onePatternMatrixElement.EndTime = nil
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}

func (c *ResultController) applyShimForTrendQuery(ctx *fiber.Ctx, queryResult *models.TrendQueryResult) (*shims.TrendQueryResult, error) {
	results := &shims.TrendQueryResult{
		Trend: make(map[string]*shims.StageTrend),
	}
	for _, stageTrend := range queryResult.Trends {
		stage, err := c.StageService.GetStageById(ctx, stageTrend.StageID)
		if err != nil {
			return nil, err
		}
		shimStageTrend := shims.StageTrend{
			Results: make(map[string]*shims.OneItemTrend),
		}
		for _, itemTrend := range stageTrend.Results {
			item, err := c.ItemService.GetItemById(ctx, itemTrend.ItemID)
			if err != nil {
				return nil, err
			}
			shimStageTrend.Results[item.ArkItemID] = &shims.OneItemTrend{
				Quantity:  itemTrend.Quantity,
				Times:     itemTrend.Times,
				StartTime: itemTrend.StartTime.UnixMilli(),
			}
		}
		results.Trend[stage.ArkStageID] = &shimStageTrend
	}
	return results, nil
}

func (c *ResultController) handleAdvancedQuery(ctx *fiber.Ctx, query *types.AdvancedQuery) (interface{}, error) {
	// handle isPersonal (might be null) and account
	isPersonal := false
	if query.IsPersonal != nil && query.IsPersonal.Valid {
		isPersonal = query.IsPersonal.Bool
	}
	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromAuthHeader(ctx, ctx.Get("Authorization"))
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
	startTime_milli := constants.ServerStartTimeMapMilli[query.Server]
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
		dropMatrixQueryResult, err := c.DropMatrixService.QueryDropMatrix(
			ctx,
			query.Server,
			[]*models.TimeRange{{StartTime: &startTime, EndTime: &endTime}},
			[]int{stage.StageID},
			itemIds,
			&accountId,
		)
		if err != nil {
			return nil, err
		}
		shimDropMatrixQueryResult, err := c.applyShimForDropMatrixQuery(ctx, query.Server, true, "", "", dropMatrixQueryResult)
		if err != nil {
			return nil, err
		}
		return shimDropMatrixQueryResult, nil
	} else {
		intervalLength_hrs := int(query.Interval.Int64 / (1000 * 60 * 60))
		if intervalLength_hrs == 0 {
			return nil, fmt.Errorf("interval length must be greater than 1 hour")
		}
		intervalNum := c.calcIntervalNum(startTime, endTime, intervalLength_hrs)
		if intervalNum > constants.MaxIntervalNum {
			return nil, fmt.Errorf("intervalNum too large")
		}

		trendQueryResult, err := c.TrendService.QueryTrend(
			ctx,
			query.Server,
			&startTime,
			intervalLength_hrs,
			intervalNum,
			[]int{stage.StageID},
			itemIds,
			&accountId,
		)
		if err != nil {
			return nil, err
		}
		shimTrendQueryResult, err := c.applyShimForTrendQuery(ctx, trendQueryResult)
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
