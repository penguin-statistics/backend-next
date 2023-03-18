package cachectrl

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

func OptIn(ctx *fiber.Ctx, lastModifiedAt time.Time) {
	offset := time.Minute * 5
	OptInCustom(ctx, lastModifiedAt, offset)
}

func OptInCustom(ctx *fiber.Ctx, lastModifiedAt time.Time, offset time.Duration) {
	ctx.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(offset.Seconds())))
	ctx.Set("Expires", lastModifiedAt.Add(offset).Format(time.RFC1123))

	ctx.Response().Header.SetLastModified(lastModifiedAt)
}

func OptOut(ctx *fiber.Ctx) {
	ctx.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.Set("Pragma", "no-cache")
	ctx.Set("Expires", "0")
}
