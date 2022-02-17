package utils

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func ConfigureLogger(config *config.Config) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	_ = os.Mkdir("logs", os.ModePerm)

	logFile, err := os.OpenFile("logs/app.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to open log file")
	}

	if config.DevMode {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	writer := zerolog.MultiLevelWriter(
		logFile,
		zerolog.ConsoleWriter{Out: os.Stdout},
	)

	log.Logger = zerolog.New(writer).With().Timestamp().Logger()
}
