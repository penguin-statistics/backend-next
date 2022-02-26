package controllers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/server"
)

type MetaController struct{}

func RegisterMetaController(v3 *server.V3) {
	c := &MetaController{}

	v3.Get("/meta/bininfo", c.BinInfo)
}

func (c *MetaController) BinInfo(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"git": fiber.Map{
			"tag":    bininfo.GitTag,
			"commit": bininfo.GitCommit,
		},
		"build": fiber.Map{
			"time": bininfo.BuildTime,
		},
	})
}
