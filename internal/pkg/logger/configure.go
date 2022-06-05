package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func Configure(conf *config.Config) {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	_ = os.Mkdir("logs", os.ModePerm)

	var level zerolog.Level
	if conf.DevMode {
		level = zerolog.TraceLevel
	} else {
		level = zerolog.DebugLevel
	}

	writer := zerolog.MultiLevelWriter(
		&lumberjack.Logger{
			Filename: "logs/app.log",
			MaxSize:  100, // megabytes
			MaxAge:   90,  // days
			Compress: true,
		},
		zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339Nano,
		},
	)

	log.Logger = zerolog.New(writer).
		With().
		Timestamp().
		Caller().
		Logger().
		Level(level)
}
