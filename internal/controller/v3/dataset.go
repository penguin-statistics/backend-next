package v3

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type Dataset struct {
	fx.In

	AccountService    *service.Account
	DropMatrixService *service.DropMatrix
}

func RegisterDataset(v3 *svr.V3, c Dataset) {
	dataset := v3.Group("/dataset")
	aggregated := dataset.Group("/aggregated/:source/:server")
	aggregated.Get("/items/:itemId", c.AggregatedItem)
	aggregated.Get("/stages/:stageId", c.AggregatedStage)
}

func (c *Dataset) AggregatedItem(ctx *fiber.Ctx) error {
	server := ctx.Params("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	isPersonal := ctx.Params("source") == "personal"

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.DropMatrixService.GetMaxAccumulableDropMatrixResultsForV3(ctx.Context(), server, "", ctx.Params("itemId"), accountId)
	if err != nil {
		return err
	}

	return ctx.JSON(queryResult)
}

func (c *Dataset) AggregatedStage(ctx *fiber.Ctx) error {
	server := ctx.Params("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	isPersonal := ctx.Params("source") == "personal"

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.DropMatrixService.GetMaxAccumulableDropMatrixResultsForV3(ctx.Context(), server, "stageId", "", accountId)
	if err != nil {
		return err
	}

	return ctx.JSON(queryResult)
}
