package v2

import (
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
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
	result := v2.Group("/_private/result")
	result.Get("/matrix/:server/:source/:category?", middlewares.ValidateServerAsParam, middlewares.ValidateCategoryAsParam, c.GetDropMatrix)
	result.Get("/pattern/:server/:source/:category?", middlewares.ValidateServerAsParam, middlewares.ValidateCategoryAsParam, c.GetPatternMatrix)
	result.Get("/trend/:server", middlewares.ValidateServerAsParam, c.GetTrends)
}

// @Summary  Get Drop Matrix
// @Tags     Private
// @Produce  json
// @Param    server    path      string  true   "Server; default to CN"                  Enums(CN, US, JP, KR)
// @Param    source    path      string  true   "Global or Personal; default to global"  Enums(global, personal)
// @Param    category  path      string  false  "Category; default to all"               Enums(all, automated, manual)
// @Success  200       {object}  modelv2.DropMatrixQueryResult
// @Failure  500       {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/_private/result/matrix/{server}/{source}/{category} [GET]
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

	shimResult, err := c.DropMatrixService.GetShimDropMatrix(ctx.UserContext(), server, true, "", "", accountId, category)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		key := server + constant.CacheSep + "true" + constant.CacheSep + category
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

// @Summary  Get Pattern Matrix
// @Tags     Private
// @Produce  json
// @Param    server    path      string  true   "Server; default to CN"                  Enums(CN, US, JP, KR)
// @Param    source    path      string  true   "Global or Personal; default to global"  Enums(global, personal)
// @Param    category  path      string  false  "Category; default to all"               Enums(all, automated, manual)
// @Success  200       {object}  modelv2.PatternMatrixQueryResult
// @Failure  500       {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/_private/result/pattern/{server}/{source}/{category} [GET]
func (c *Private) GetPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	isPersonal := ctx.Params("source") == "personal"
	category := ctx.Params("category", "all")
	showAllPatterns := false

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimResult, err := c.PatternMatrixService.GetShimPatternMatrix(ctx.UserContext(), server, accountId, category, showAllPatterns)
	if err != nil {
		return err
	}

	if !accountId.Valid {
		key := server + constant.CacheSep + category + constant.CacheSep + strconv.FormatBool(showAllPatterns)
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+key+"]", &lastModifiedTime); err != nil {
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
	shimResult, err := c.TrendService.GetShimTrend(ctx.UserContext(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimTrend#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)

	return ctx.JSON(shimResult)
}
