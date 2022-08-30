package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/pkg/middlewares"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

var _ modelv2.Dummy

type Private struct {
	fx.In

	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	AccountService       *service.Account
	ItemService          *service.Item
	StageService         *service.Stage
}

func RegisterPrivate(v2 *svr.V2, c Private) {
	v2.Get("/_private/result/matrix/:server/:source/:category?", middlewares.ValidateServerAsParam, middlewares.ValidateCategoryAsParam, c.GetDropMatrix)
	v2.Get("/_private/result/pattern/:server/:source/:category?", middlewares.ValidateServerAsParam, middlewares.ValidateCategoryAsParam, c.GetPatternMatrix)
	v2.Get("/_private/result/trend/:server", middlewares.ValidateServerAsParam, c.GetTrends)
}

// @Summary  Get Drop Matrix
// @Tags     Private
// @Produce  json
// @Param    server    path      string  true  "Server; default to CN"                  Enums(CN, US, JP, KR)
// @Param    source    path      string  true  "Global or Personal; default to global"  Enums(global, personal)
// @Param    category  path      string  true  "Category; default to all"               Enums(all, automated, manual)
// @Success  200       {object}  modelv2.DropMatrixQueryResult
// @Failure  500       {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/_private/result/matrix/{server}/{source} [GET]
func (c *Private) GetDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	isPersonal := ctx.Params("source") == "personal"
	category := ctx.Params("category", "all")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimResult, err := c.DropMatrixService.GetShimMaxAccumulableDropMatrixResults(ctx.UserContext(), server, true, "", "", accountId, category)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		key := server + constant.CacheSep + "true" + constant.CacheSep + category
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimMaxAccumulableDropMatrixResults#server|showClosedZoned|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

// @Summary  Get Pattern Matrix
// @Tags     Private
// @Produce  json
// @Param    server    path      string  true  "Server; default to CN"                  Enums(CN, US, JP, KR)
// @Param    source    path      string  true  "Global or Personal; default to global"  Enums(global, personal)
// @Param    category  path      string  true  "Category; default to all"               Enums(all, automated, manual)
// @Success  200       {object}  modelv2.PatternMatrixQueryResult
// @Failure  500       {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/_private/result/pattern/{server}/{source} [GET]
func (c *Private) GetPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	isPersonal := ctx.Params("source") == "personal"
	category := ctx.Params("category", "all")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimResult, err := c.PatternMatrixService.GetShimLatestPatternMatrixResults(ctx.UserContext(), server, accountId, category)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		key := server + constant.CacheSep + category
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimLatestPatternMatrixResults#server|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

// @Summary  Get Trends
// @Tags     Private
// @Produce  json
// @Param    server  path      string  true  "Server; default to CN"  Enums(CN, US, JP, KR)
// @Success  200     {object}  modelv2.TrendQueryResult
// @Failure  500     {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/_private/result/trend/{server} [GET]
func (c *Private) GetTrends(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
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
