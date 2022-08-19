package v3

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/jinzhu/copier"
	"github.com/samber/lo"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	modelv3 "github.com/penguin-statistics/backend-next/internal/model/v3"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type Dataset struct {
	fx.In

	AccountService       *service.Account
	DropMatrixService    *service.DropMatrix
	TrendService         *service.Trend
	PatternMatrixService *service.PatternMatrix
}

func RegisterDataset(v3 *svr.V3, c Dataset) {
	dataset := v3.Group("/dataset")
	aggregated := dataset.Group("/aggregated/:source/:server")
	aggregated.Get("/item/:itemId", c.AggregatedItem)
	aggregated.Get("/stage/:stageId", c.AggregatedStage)
}

func (c Dataset) aggregateMatrix(ctx *fiber.Ctx) (*modelv2.DropMatrixQueryResult, error) {
	server := ctx.Params("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return nil, err
	}

	isPersonal := ctx.Params("source") == "personal"

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return nil, err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.DropMatrixService.GetMaxAccumulableDropMatrixResults(ctx.UserContext(), server, "", ctx.Params("itemId"), accountId)
	if err != nil {
		return nil, err
	}

	return queryResult, nil
}

func (c Dataset) aggregateTrend(ctx *fiber.Ctx) (*modelv2.TrendQueryResult, error) {
	server := ctx.Params("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return nil, err
	}

	result, err := c.TrendService.GetShimSavedTrendResults(ctx.UserContext(), server)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (c Dataset) aggregatePattern(ctx *fiber.Ctx) (*modelv3.PatternMatrixQueryResult, error) {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return nil, err
	}

	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return nil, err
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

	shimResult, err := c.PatternMatrixService.GetShimLatestPatternMatrixResults(ctx.UserContext(), server, accountId)
	if err != nil {
		return nil, err
	}

	var result modelv3.PatternMatrixQueryResult
	copier.Copy(&result, shimResult)

	return &result, nil
}

func (c Dataset) AggregatedItem(ctx *fiber.Ctx) error {
	aggregated := &modelv3.AggregatedItemStats{}
	itemId := ctx.Params("itemId")

	matrix, err := c.aggregateMatrix(ctx)
	if err != nil {
		return err
	}
	aggregated.Matrix = lo.Filter(matrix.Matrix, func(el *modelv2.OneDropMatrixElement, _ int) bool {
		return el.ItemID == itemId
	})

	trend, err := c.aggregateTrend(ctx)
	if err != nil {
		return err
	}
	aggregated.Trends = make(map[string]*modelv2.StageTrend)
	for stageId, v := range trend.Trend {
		for itemId, vv := range v.Results {
			if itemId != ctx.Params("itemId") {
				continue
			}
			if _, ok := aggregated.Trends[stageId]; !ok {
				aggregated.Trends[stageId] = &modelv2.StageTrend{
					StartTime: v.StartTime,
					Results:   make(map[string]*modelv2.OneItemTrend),
				}
			}
			aggregated.Trends[stageId].Results[itemId] = vv
		}
	}

	return ctx.JSON(aggregated)
}

func (c Dataset) AggregatedStage(ctx *fiber.Ctx) error {
	aggregated := &modelv3.AggregatedStageStats{}

	matrix, err := c.aggregateMatrix(ctx)
	if err != nil {
		return err
	}
	aggregated.Matrix = lo.Filter(matrix.Matrix, func(el *modelv2.OneDropMatrixElement, _ int) bool {
		return el.StageID == ctx.Params("stageId")
	})

	trend, err := c.aggregateTrend(ctx)
	if err != nil {
		return err
	}
	aggregated.Trends = make(map[string]*modelv2.StageTrend)
	for stageId, v := range trend.Trend {
		if stageId != ctx.Params("stageId") {
			continue
		}
		for itemId, vv := range v.Results {
			if _, ok := aggregated.Trends[stageId]; !ok {
				aggregated.Trends[stageId] = &modelv2.StageTrend{
					StartTime: v.StartTime,
					Results:   make(map[string]*modelv2.OneItemTrend),
				}
			}
			aggregated.Trends[stageId].Results[itemId] = vv
		}
	}

	pattern, err := c.aggregatePattern(ctx)
	if err != nil {
		return err
	}
	aggregated.Patterns = lo.Filter(pattern.PatternMatrix, func(el *modelv3.OnePatternMatrixElement, _ int) bool {
		return el.StageID == ctx.Params("stageId")
	})

	return ctx.JSON(aggregated)
}
