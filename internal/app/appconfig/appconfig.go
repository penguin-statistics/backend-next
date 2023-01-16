package appconfig

import (
	"fmt"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appcontext"
	"exusiai.dev/backend-next/internal/pkg/projectpath"
)

func Parse(ctx appcontext.Ctx) (*Config, error) {
	err := godotenv.Load(filepath.Join(projectpath.Root, ".env"))
	if err != nil {
		log.Warn().Err(err).Msg("failed to load .env file")
	}

	var config ConfigSpec
	err = envconfig.Process("penguin_v3", &config)
	if err != nil {
		_ = envconfig.Usage("penguin_v3", &config)
		return nil, fmt.Errorf("failed to parse configuration: %w. More info on how to configure this backend is located at https://pkg.go.dev/exusiai.dev/backend-next/internal/config#Config", err)
	}

	return &Config{
		ConfigSpec: config,
		AppContext: ctx,
	}, nil
}
