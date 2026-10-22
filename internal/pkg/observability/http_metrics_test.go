package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestHTTPStatusClass(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "other"},
		{100, "1xx"},
		{200, "2xx"},
		{304, "3xx"},
		{404, "4xx"},
		{499, "4xx"},
		{500, "5xx"},
		{599, "5xx"},
		{600, "other"},
	}
	for _, tt := range tests {
		if got := HTTPStatusClass(tt.code); got != tt.want {
			t.Errorf("HTTPStatusClass(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestHTTPStatusCode(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{-1, "other"},
		{0, "other"},
		{99, "other"},
		{100, "100"},
		{200, "200"},
		{304, "304"},
		{499, "499"},
		{599, "599"},
		{600, "other"},
	}
	for _, tt := range tests {
		if got := HTTPStatusCode(tt.code); got != tt.want {
			t.Errorf("HTTPStatusCode(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
	if n := testing.AllocsPerRun(100, func() { _ = HTTPStatusCode(304) }); n != 0 {
		t.Errorf("HTTPStatusCode allocates %v times, want 0", n)
	}
}

// TestObserveHTTPResponse checks that a response is recorded in both histograms under its labels, a status out of
// range under other, and that recording a response whose labels were seen before allocates nothing.
func TestObserveHTTPResponse(t *testing.T) {
	for _, tc := range []struct {
		status            int
		class, statusCode string
	}{
		{304, "3xx", "304"},
		{999, "other", "other"},
	} {
		size := HTTPResponseSize.WithLabelValues("/test/observe", "GET", tc.class)
		duration := HTTPRequestDuration.WithLabelValues("/test/observe", "GET", tc.statusCode)
		sizeBefore, durationBefore := histogram(t, size), histogram(t, duration)

		ObserveHTTPResponse("/test/observe", "GET", tc.status, 512, 1500*time.Millisecond)

		if s := histogram(t, size); s.GetSampleCount()-sizeBefore.GetSampleCount() != 1 || s.GetSampleSum()-sizeBefore.GetSampleSum() != 512 {
			t.Errorf("status %d: response_size_bytes{%s} observed %d responses summing %v, want 1 of 512", tc.status, tc.class,
				s.GetSampleCount()-sizeBefore.GetSampleCount(), s.GetSampleSum()-sizeBefore.GetSampleSum())
		}
		if d := histogram(t, duration); d.GetSampleCount()-durationBefore.GetSampleCount() != 1 || d.GetSampleSum()-durationBefore.GetSampleSum() != 1.5 {
			t.Errorf("status %d: request_duration_seconds{%s} observed %d requests summing %v, want 1 of 1.5", tc.status, tc.statusCode,
				d.GetSampleCount()-durationBefore.GetSampleCount(), d.GetSampleSum()-durationBefore.GetSampleSum())
		}
	}
	if n := testing.AllocsPerRun(100, func() { ObserveHTTPResponse("/test/observe", "GET", 304, 512, time.Millisecond) }); n != 0 {
		t.Errorf("ObserveHTTPResponse allocates %v times, want 0", n)
	}
}

func histogram(t *testing.T, o prometheus.Observer) *dto.Histogram {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram()
}
