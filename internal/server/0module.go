package server

import (
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/server/svr"
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("server",
		fx.Provide(httpserver.Create),
		fx.Provide(svr.CreateEndpointGroups))
}
