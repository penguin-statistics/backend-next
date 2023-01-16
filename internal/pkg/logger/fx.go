package logger

import (
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx/fxevent"
)

type fxLogger struct {
	l zerolog.Logger
}

var _ io.Writer = (*fxLogger)(nil)

func Fx() fxevent.Logger {
	logger := fxLogger{
		l: log.Logger.
			With().
			Str("evt.name", "fx.init").
			Logger(),
	}
	return &fxevent.ConsoleLogger{
		W: logger,
	}
}

func (l fxLogger) Write(p []byte) (n int, err error) {
	// from https://github.com/rs/zerolog/blob/a9a8199d2dd3578d37e459618515f34b5e917f8d/log.go#L435-L441
	n = len(p)
	if n > 0 && p[n-1] == '\n' {
		// Trim CR added by stdlog.
		p = p[0 : n-1]
	}
	l.l.Info().CallerSkipFrame(0).Msg(string(p))
	return
}
