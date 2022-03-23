package controllers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
)

type MetaController struct{}

func RegisterMetaController(meta *svr.Meta) {
	c := &MetaController{}

	meta.Get("/bininfo", c.BinInfo)
}

func (c *MetaController) BinInfo(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version": bininfo.Version,
		"build":   bininfo.BuildTime,
	})
}
