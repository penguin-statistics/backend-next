package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// httpKey is the labels of a response in HTTPResponseSize and HTTPRequestDuration, but for the status: its code.
type httpKey struct {
	route, method string
	status        int
}

type httpObservers struct {
	size, duration prometheus.Observer
}

// httpObserverCache holds the observers of the responses by their labels. Looking them up by WithLabelValues on every
// request allocates (its variadic labels escape), while this map hashes its key in place. It stays bounded with the
// labels: the routes and methods are those of the app, and the status codes are bounded by HTTPStatusCode.
var httpObserverCache = struct {
	sync.RWMutex
	m map[httpKey]*httpObservers
}{m: map[httpKey]*httpObservers{}}

// ObserveHTTPResponse records a response of route (the matched route, see middlewares.requestLogger) to method with
// status: its body size in HTTPResponseSize and the duration of its request in HTTPRequestDuration.
func ObserveHTTPResponse(route, method string, status, size int, duration time.Duration) {
	if status < 100 || status >= len(statusCodes) {
		// one key for every code out of range, as they share their labels
		status = 0
	}
	k := httpKey{route, method, status}
	httpObserverCache.RLock()
	o := httpObserverCache.m[k]
	httpObserverCache.RUnlock()
	if o == nil {
		o = &httpObservers{
			size:     HTTPResponseSize.WithLabelValues(route, method, HTTPStatusClass(status)),
			duration: HTTPRequestDuration.WithLabelValues(route, method, HTTPStatusCode(status)),
		}
		httpObserverCache.Lock()
		httpObserverCache.m[k] = o
		httpObserverCache.Unlock()
	}
	o.size.Observe(float64(size))
	o.duration.Observe(duration.Seconds())
}
