package httpserver

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

// The request logger runs before the ErrorHandler has written the response
// for a returned error; it only sees the right status because otelfiber, the
// innermost app middleware, calls the ErrorHandler first. This test pins that
// down on the real middleware chain, together with the bounded route labels of
// paths no route matches.
func TestServiceAppObservesResponseByStatusClassAndRoute(t *testing.T) {
	app := CreateServiceApp(&appconfig.Config{})
	app.Get("/fiber-error", func(ctx *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "bad")
	})
	app.Get("/penguin-error", func(ctx *fiber.Ctx) error {
		return pgerr.ErrInvalidReq
	})
	app.Get("/generic-error", func(ctx *fiber.Ctx) error {
		return errors.New("boom")
	})
	// like the v2 group in svr.CreateEndpointGroups
	v2 := app.Group("/PenguinStats/api/v2", func(ctx *fiber.Ctx) error {
		return ctx.Next()
	})
	v2.Get("/stages/:stageId", func(ctx *fiber.Ctx) error {
		return ctx.SendString("ok")
	})

	tests := []struct {
		url         string
		status      int
		route       string
		statusClass string
	}{
		{"/PenguinStats/api/v2/stages/main_01-07", fiber.StatusOK, "/PenguinStats/api/v2/stages/:stageId", "2xx"},
		{"/fiber-error", fiber.StatusBadRequest, "/fiber-error", "4xx"},
		{"/penguin-error", fiber.StatusBadRequest, "/penguin-error", "4xx"},
		{"/generic-error", fiber.StatusInternalServerError, "/generic-error", "5xx"},
		// a path no route matches is labelled with the last app or group
		// middleware prefix it passed, never the raw URL
		{"/wp-admin/x.php", fiber.StatusNotFound, "/", "4xx"},
		{"/PenguinStats/api/v2/no-such-route", fiber.StatusNotFound, "/PenguinStats/api/v2", "4xx"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			countBefore, sumBefore := responseSize(t, tt.route, "GET", tt.statusClass)
			resp, err := app.Test(httptest.NewRequest("GET", tt.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.status {
				t.Fatalf("response status = %d, want %d", resp.StatusCode, tt.status)
			}
			countAfter, sumAfter := responseSize(t, tt.route, "GET", tt.statusClass)

			if got := countAfter - countBefore; got != 1 {
				t.Errorf("response count under route %q, class %s increased by %d, want 1", tt.route, tt.statusClass, got)
			}
			if sumAfter == sumBefore {
				t.Errorf("response size sum did not increase, want the size of the body")
			}
		})
	}
}

// responseSize returns the sample count and sum of the response size histogram
// for the given labels.
func responseSize(t *testing.T, route, method, statusClass string) (uint64, float64) {
	t.Helper()

	var m dto.Metric
	h := observability.HTTPResponseSize.WithLabelValues(route, method, statusClass).(prometheus.Metric)
	if err := h.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
}
