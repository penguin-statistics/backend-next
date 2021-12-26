package utils

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

func ConfigureLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	_ = os.Mkdir("logs", os.ModePerm)

	logFile, err := os.OpenFile("logs/app.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to open log file")
	}

	writer := zerolog.MultiLevelWriter(
		logFile,
		zerolog.ConsoleWriter{Out: os.Stdout},
	)

	log.Logger = zerolog.New(writer).With().Timestamp().Logger()
}
