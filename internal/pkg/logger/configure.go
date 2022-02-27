package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func Configure(config *config.Config) {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	_ = os.Mkdir("logs", os.ModePerm)

	logFile, err := os.OpenFile("logs/app.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		log.Panic().Err(err).Msg("failed to open log file")
	}

	var level zerolog.Level
	if config.DevMode {
		level = zerolog.TraceLevel
	} else {
		level = zerolog.DebugLevel
	}

	writer := zerolog.MultiLevelWriter(
		logFile,
		zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339Nano,
		},
	)

	log.Logger = zerolog.New(writer).
		With().
		Timestamp().
		Logger().
		Level(level)
}
