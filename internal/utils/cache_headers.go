package utils

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

func SetCtxCacheHeaders(ctx *fiber.Ctx, cacheTime time.Duration) {
	ctx.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(cacheTime.Seconds())))
	ctx.Set("Expires", time.Now().Add(cacheTime).Format(time.RFC1123))
}

func SetCtxNoCache(ctx *fiber.Ctx) {
	ctx.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.Set("Pragma", "no-cache")
	ctx.Set("Expires", "0")
}
