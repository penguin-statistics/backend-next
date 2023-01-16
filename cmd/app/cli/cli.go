package cli

import (
	"context"

	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app"
	"exusiai.dev/backend-next/internal/app/appcontext"
)

func Start(module fx.Option) {
	app.New(appcontext.Declare(appcontext.EnvCLI), module).Start(context.Background())
}
