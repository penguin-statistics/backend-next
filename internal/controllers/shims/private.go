package shims

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type PrivateController struct {
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	AccountService       *service.AccountService
	ItemService          *service.ItemService
	StageService         *service.StageService
}

func RegisterPrivateController(
	v2 *server.V2,
	dropMatrixService *service.DropMatrixService,
	patternMatrixService *service.PatternMatrixService,
	trendService *service.TrendService,
	accountService *service.AccountService,
	itemService *service.ItemService,
	stageService *service.StageService,
) {
	c := &PrivateController{
		DropMatrixService:    dropMatrixService,
		PatternMatrixService: patternMatrixService,
		TrendService:         trendService,
		AccountService:       accountService,
		ItemService:          itemService,
		StageService:         stageService,
	}

	v2.Get("/_private/result/matrix/:server/:source", c.GetDropMatrix)
	v2.Get("/_private/result/pattern/:server/:source", c.GetPatternMatrix)
	v2.Get("/_private/result/trend/:server", c.GetTrends)
}

// @Summary      Get DropMatrix
// @Tags         Private
// @Produce      json
// @Param        server            path     string   "CN"     "Server"
// @Param        source            path     string   "global" "Global or Personal"
// @Success      200               {object} shims.DropMatrixQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/_private/result/matrix/{server}/{source} [GET]
// @Deprecated
func (c *PrivateController) GetDropMatrix(ctx *fiber.Ctx) error {
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Params("server")
	isPersonal := ctx.Params("source") == "personal"

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

	shimResult, err := c.DropMatrixService.GetShimMaxAccumulableDropMatrixResults(ctx, server, true, "", "", &accountId)
	if err != nil {
		return err
	}
	return ctx.JSON(shimResult)
}

// @Summary      Get PatternMatrix
// @Tags         Private
// @Produce      json
// @Param        server            path     string   "CN"     "Server"
// @Param        source            path     string   "global" "Global or Personal"
// @Success      200               {object} shims.PatternMatrixQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/_private/result/pattern/{server}/{source} [GET]
// @Deprecated
func (c *PrivateController) GetPatternMatrix(ctx *fiber.Ctx) error {
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Params("server")
	isPersonal := ctx.Params("source") == "personal"

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

	shimResult, err := c.PatternMatrixService.GetShimLatestPatternMatrixResults(ctx, server, &accountId)
	if err != nil {
		return err
	}
	return ctx.JSON(shimResult)
}

// @Summary      Get Trends
// @Tags         Private
// @Produce      json
// @Param        server            path     string   "CN"     "Server"
// @Success      200               {object} shims.TrendQueryResult
// @Failure      500               {object} errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/_private/result/trend/{server} [GET]
// @Deprecated
func (c *PrivateController) GetTrends(ctx *fiber.Ctx) error {
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Params("server")
	shimResult, err := c.TrendService.GetShimSavedTrendResults(ctx, server)
	if err != nil {
		return err
	}
	return ctx.JSON(shimResult)
}
