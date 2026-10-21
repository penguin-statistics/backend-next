package v2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

// newAdvancedQueryMetricsTestApp returns an app answering POST /advanced with handlers, and errors the way the
// production app does.
func newAdvancedQueryMetricsTestApp(handlers ...fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httpserver.ErrorHandler})
	app.Post("/advanced", handlers...)
	return app
}

func postAdvanced(t *testing.T, app *fiber.App, body string) *http.Response {
	t.Helper()
	return post(t, app, "/advanced", body)
}

func post(t *testing.T, app *fiber.App, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, path, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func histogramCountAndSum(t *testing.T, o prometheus.Observer) (uint64, float64) {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
}

func TestObserveAdvancedQueryInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseHandler)
	app := newAdvancedQueryMetricsTestApp(observeAdvancedQuery, func(ctx *fiber.Ctx) error {
		close(entered)
		<-release
		return nil
	})

	before := testutil.ToFloat64(observability.AdvancedQueryInFlight)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := app.Test(httptest.NewRequest(fiber.MethodPost, "/advanced", nil), -1)
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
	}()

	select {
	case <-entered:
	case <-done:
		t.Fatal("request finished without reaching the handler")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the request to reach the handler")
	}
	if got := testutil.ToFloat64(observability.AdvancedQueryInFlight); got != before+1 {
		t.Errorf("in flight while handling: got %v, want %v", got, before+1)
	}
	releaseHandler()
	<-done
	if got := testutil.ToFloat64(observability.AdvancedQueryInFlight); got != before {
		t.Errorf("in flight after handling: got %v, want %v", got, before)
	}
}

func TestObserveAdvancedQueryRequestsByOutcome(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int // written by the handler itself, which then returns nil
		outcome string
	}{
		{"ok", nil, 0, advancedQueryOutcomeOK},
		{"invalid request", pgerr.ErrInvalidReq.Msg("too many sections"), 0, advancedQueryOutcomeClientError},
		{"not found", pgerr.ErrNotFound, 0, advancedQueryOutcomeClientError},
		{"fiber client error", fiber.ErrRequestEntityTooLarge, 0, advancedQueryOutcomeClientError},
		{"internal error", pgerr.ErrInternalError, 0, advancedQueryOutcomeError},
		{"unexpected error", errors.New("query failed"), 0, advancedQueryOutcomeError},
		{"client went away", context.Canceled, 0, advancedQueryOutcomeError},
		{"written client error", nil, fiber.StatusTooManyRequests, advancedQueryOutcomeClientError},
		{"written error", nil, fiber.StatusServiceUnavailable, advancedQueryOutcomeError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newAdvancedQueryMetricsTestApp(observeAdvancedQuery, func(ctx *fiber.Ctx) error {
				if tt.err != nil {
					return tt.err
				}
				if tt.status != 0 {
					ctx.Status(tt.status)
				}
				return ctx.JSON(fiber.Map{})
			})

			before := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(tt.outcome))
			resp := postAdvanced(t, app, "{}")
			if got := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(tt.outcome)); got != before+1 {
				t.Errorf("requests with outcome %q: got %v, want %v", tt.outcome, got, before+1)
			}

			// the outcome must agree with the status the client is answered with
			wantOutcome := advancedQueryOutcomeOK
			if resp.StatusCode >= 500 {
				wantOutcome = advancedQueryOutcomeError
			} else if resp.StatusCode >= 400 {
				wantOutcome = advancedQueryOutcomeClientError
			}
			if tt.outcome != wantOutcome {
				t.Errorf("status %d is outcome %q, not %q", resp.StatusCode, wantOutcome, tt.outcome)
			}
		})
	}
}

func TestObserveAdvancedQueryDuration(t *testing.T) {
	const took = 50 * time.Millisecond
	app := newAdvancedQueryMetricsTestApp(observeAdvancedQuery, func(ctx *fiber.Ctx) error {
		time.Sleep(took)
		return pgerr.ErrInvalidReq
	})

	observer := observability.AdvancedQueryDuration.WithLabelValues(advancedQueryOutcomeClientError)
	beforeCount, beforeSum := histogramCountAndSum(t, observer)
	postAdvanced(t, app, "{}")
	count, sum := histogramCountAndSum(t, observer)
	if count != beforeCount+1 {
		t.Errorf("duration samples: got %d, want %d", count, beforeCount+1)
	}
	if sum-beforeSum < took.Seconds() {
		t.Errorf("observed duration %vs, want at least %vs", sum-beforeSum, took.Seconds())
	}
}

func TestObserveAdvancedQueryPanic(t *testing.T) {
	app := newAdvancedQueryMetricsTestApp(recover.New(), observeAdvancedQuery, func(ctx *fiber.Ctx) error {
		panic("query failed")
	})

	inFlight := testutil.ToFloat64(observability.AdvancedQueryInFlight)
	before := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(advancedQueryOutcomeError))
	resp := postAdvanced(t, app, "{}")
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
	}
	if got := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(advancedQueryOutcomeError)); got != before+1 {
		t.Errorf("requests with outcome %q: got %v, want %v", advancedQueryOutcomeError, got, before+1)
	}
	if got := testutil.ToFloat64(observability.AdvancedQueryInFlight); got != inFlight {
		t.Errorf("in flight after a panic: got %v, want %v", got, inFlight)
	}
}

// TestAdvancedQueryRoute goes through the route RegisterResult sets up, so it also fails if the route stops being
// observed.
func TestAdvancedQueryRoute(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpserver.ErrorHandler})
	app.Use(middlewares.InjectI18n())
	// personal queries without a PenguinID are rejected before any lookup, so no other service is needed
	RegisterResult(&svr.V2{Router: app.Group("/PenguinStats/api/v2")}, Result{AccountService: &service.Account{}})
	const path = "/PenguinStats/api/v2/result/advanced"
	query := `{"server":"CN","stageId":"main_01-07","isPersonal":true}`

	before := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(advancedQueryOutcomeClientError))
	beforeCount, beforeSum := histogramCountAndSum(t, observability.AdvancedQueryQueriesPerRequest)
	resp := post(t, app, path, `{"queries":[`+query+`,`+query+`,`+query+`]}`)
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
	if got := testutil.ToFloat64(observability.AdvancedQueryRequests.WithLabelValues(advancedQueryOutcomeClientError)); got != before+1 {
		t.Errorf("requests with outcome %q: got %v, want %v", advancedQueryOutcomeClientError, got, before+1)
	}
	count, sum := histogramCountAndSum(t, observability.AdvancedQueryQueriesPerRequest)
	if count != beforeCount+1 || sum != beforeSum+3 {
		t.Errorf("queries per request: got count %d sum %v, want count %d sum %v", count, sum, beforeCount+1, beforeSum+3)
	}

	// an invalid request has no queries to count
	post(t, app, path, `{"queries":[]}`)
	if count, _ := histogramCountAndSum(t, observability.AdvancedQueryQueriesPerRequest); count != beforeCount+1 {
		t.Errorf("queries per request after an invalid request: got count %d, want %d", count, beforeCount+1)
	}
}
