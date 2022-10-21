package calcwkr

import (
	"time"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

func observeCalcDuration(service string, server string, f func() error) error {
	start := time.Now()
	defer func() {
		dur := time.Since(start)
		observability.WorkerCalcDuration.WithLabelValues(service, server).Set(float64(dur.Seconds()))
	}()
	return f()
}
