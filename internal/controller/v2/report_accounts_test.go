package v2

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
)

// TestReportAccountsMetric resolves the account of a report request without a PenguinID, with the PenguinID of the
// account of the test schema, and with one that names no account: accounts_total counts them as created, existing
// and recreated.
func TestReportAccountsMetric(t *testing.T) {
	db := newTestDB(t)
	flushCaches(t)
	c := &Report{AccountService: service.NewAccount(repo.NewAccount(db))}
	app := fiber.New()
	app.Post("/report", c.MiddlewareGetOrCreateAccount, func(ctx *fiber.Ctx) error { return ctx.SendStatus(fiber.StatusOK) })

	for _, tc := range []struct {
		name, authorization, outcome string
	}{
		{"no PenguinID", "", "created"},
		{"known PenguinID", "PenguinID 11111111", "existing"},
		{"unknown PenguinID", "PenguinID 99999999", "recreated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := observability.ReportAccounts.WithLabelValues(tc.outcome)
			before := testutil.ToFloat64(counter)
			req := httptest.NewRequest(fiber.MethodPost, "/report", nil)
			if tc.authorization != "" {
				req.Header.Set(fiber.HeaderAuthorization, tc.authorization)
			}
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status %d, want 200", resp.StatusCode)
			}
			if got := testutil.ToFloat64(counter) - before; got != 1 {
				t.Errorf("accounts_total{%s} increased by %v, want 1", tc.outcome, got)
			}
		})
	}
}
