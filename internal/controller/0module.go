package controller

import (
	"go.uber.org/fx"

	controllermcp "exusiai.dev/backend-next/internal/controller/mcp"
	controllermeta "exusiai.dev/backend-next/internal/controller/meta"
	controllerseo "exusiai.dev/backend-next/internal/controller/seo"
	controllerv2 "exusiai.dev/backend-next/internal/controller/v2"
)

type opt int

const (
	OptIncludeSwagger opt = iota
)

func Module(o ...opt) fx.Option {
	opts := []fx.Option{
		// Controllers (v2)
		controllerv2.Module(),

		// Controllers (meta)
		controllermeta.Module(),

		// Server-rendered stats pages of the frontend, if enabled
		fx.Invoke(controllerseo.Register),

		// MCP server of the drop data at /mcp, if enabled
		fx.Invoke(controllermcp.Register),
	}
	for _, opt := range o {
		switch opt {
		case OptIncludeSwagger:
			opts = append(opts, fx.Invoke(controllermeta.RegisterSwagger))
		}
	}

	return fx.Module("controller",
		// options
		opts...,
	)
}
