package logger

import (
	"github.com/rs/zerolog/log"
	"go.uber.org/fx/fxevent"
)

func Fx() fxevent.Logger {
	return &fxevent.ConsoleLogger{
		W: log.Logger.
			With().
			Str("evt.name", "fx.init").
			Logger(),
	}
}
