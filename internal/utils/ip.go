package utils

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

func ExtractIP(ctx *fiber.Ctx) string {
	ip := ctx.IP()
	splitted := strings.SplitN(ip, ",", 2)
	if len(splitted) > 0 {
		return strings.TrimSpace(splitted[0])
	} else {
		return ""
	}
}
