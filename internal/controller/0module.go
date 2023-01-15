package controller

import (
	"go.uber.org/fx"

	controllermeta "exusiai.dev/backend-next/internal/controller/meta"
	controllerv2 "exusiai.dev/backend-next/internal/controller/v2"
	controllerv3 "exusiai.dev/backend-next/internal/controller/v3"
)

type opt int

const (
	OptIncludeSwagger opt = iota
)

func Module(o ...opt) fx.Option {
	opts := []fx.Option{
		// Controllers (v2)
		controllerv2.Module(),

		// Controllers (v3)
		controllerv3.Module(),

		// Controllers (meta)
		controllermeta.Module(),
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
