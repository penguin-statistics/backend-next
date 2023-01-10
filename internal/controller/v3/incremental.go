package v3

import (
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	dtov3 "exusiai.dev/backend-next/internal/model/dto/v3"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

var ErrIncrementalInvalidVersions = pgerr.ErrInvalidReq.Msg("invalid versions: `versions` after /patch shall be two `from` and `to` versions, respectively, separated by three dots")

type IncrementalController struct {
	fx.In

	SnapshotService *service.Snapshot
}

func RegisterIncremental(v3 *svr.V3, c IncrementalController) {
	group := v3.Group("/incremental")
	group.Get("/:server/:realm/latest", c.GetLatestIncrementalVersion)
	group.Get("/:server/:realm/patch/:versions", c.GetDiffBetweenVersions)
}

func (c *IncrementalController) GetLatestIncrementalVersion(ctx *fiber.Ctx) error {
	snapshot, err := c.SnapshotService.SnapshotRepo.GetLatestSnapshotByKey(ctx.UserContext(), c.GetSnapshotKeyFromPathParams(ctx))
	if err != nil {
		return err
	}

	return ctx.JSON(dtov3.GetLatestIncrementalVersionResponse{
		Version: snapshot.Version,
	})
}

func (c *IncrementalController) GetDiffBetweenVersions(ctx *fiber.Ctx) error {
	key := c.GetSnapshotKeyFromPathParams(ctx)
	var fromVersion, toVersion string
	if versions := ctx.Params("versions"); versions == "" {
		return ErrIncrementalInvalidVersions
	} else {
		versions := strings.Split(versions, "...")
		if len(versions) != 2 {
			return ErrIncrementalInvalidVersions
		}

		fromVersion = versions[0]
		toVersion = versions[1]
		if fromVersion == "" || toVersion == "" {
			return ErrIncrementalInvalidVersions
		}
	}

	diff, err := c.SnapshotService.GetDiffBetweenVersions(ctx.UserContext(), key, fromVersion, toVersion)
	if err != nil {
		return err
	}

	if len(diff) == 0 {
		return ctx.SendStatus(fiber.StatusNoContent)
	}

	cachectrl.OptInCustom(ctx, time.Now(), time.Hour*24*365)

	return ctx.Send(diff)
}

func (c *IncrementalController) GetSnapshotKeyFromPathParams(ctx *fiber.Ctx) string {
	return ctx.Params("server") + constant.CacheSep + ctx.Params("realm")
}
