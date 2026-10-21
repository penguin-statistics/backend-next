package calcwkr

import (
	"time"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// observeCalcDuration runs f, records the duration it took as the last of service on server and returns it.
func observeCalcDuration(service string, server string, f func() error) (dur time.Duration, err error) {
	start := time.Now()
	defer func() {
		dur = time.Since(start)
		observability.WorkerCalcDuration.WithLabelValues(service, server).Set(float64(dur.Seconds()))
	}()
	return 0, f()
}
