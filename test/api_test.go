package test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dchest/uniuri"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app"
	"exusiai.dev/backend-next/internal/app/appcontext"
)

// testing hooks: https://pkg.go.dev/testing#hdr-Subtests_and_Sub_benchmarks

var (
	gMu       sync.Mutex
	gFiberApp *fiber.App
)

func startup(t *testing.T) {
	t.Helper()

	gMu.Lock()
	defer gMu.Unlock()

	if gFiberApp != nil {
		return
	}

	var fiberApp *fiber.App
	fxApp := fxtest.New(t,
		append(app.Options(appcontext.Declare(appcontext.EnvServer)), fx.Populate(&fiberApp))...,
	)
	fxApp.RequireStart()

	gFiberApp = fiberApp
}

func request(t *testing.T, req *http.Request, msTimeout ...int) *http.Response {
	t.Helper()

	resp, err := gFiberApp.Test(req, msTimeout...)
	if err != nil {
		t.Fatal(err)
	}

	return resp
}

func TestAPIMeta(t *testing.T) {
	startup(t)
	t.Parallel()

	t.Run("health", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/api/_/health", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("version", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/api/_/bininfo", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// TestAPIV2Resources tests the resources API except the reports API.
func TestAPIV2Resources(t *testing.T) {
	startup(t)
	t.Parallel()

	t.Run("items", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/items", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stages", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/stages", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("zones", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/zones", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestAPIV2Reports(t *testing.T) {
	startup(t)
	t.Parallel()

	// helpers
	reportCustom := func(req *http.Request) (*http.Response, *gjson.Result) {
		t.Helper()

		resp := request(t, req)

		bodyBytes, err := io.ReadAll(resp.Body)
		assert.NoError(t, err, "failed to read response body")

		body := gjson.ParseBytes(bodyBytes)

		return resp, &body
	}

	report := func(body string, headers *http.Header) (*http.Response, *gjson.Result) {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, "/PenguinStats/api/v2/report", bytes.NewBufferString(body))
		if headers != nil {
			req.Header = *headers
		}
		req.Header.Set("Content-Type", "application/json")
		return reportCustom(req)
	}

	// tests
	t.Run("body: valid", func(t *testing.T) {
		h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
		assert.Equal(t, http.StatusOK, h.StatusCode, "status code should be 200 but got unexpected value. body: %s", j.String())
		assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
		// TODO: check DB for the report correctness
	})

	t.Run("body: invalid", func(t *testing.T) {
		t.Run("invalid json", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode, "should return 400, got %d", h.StatusCode, "body: %s", j.String())
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("invalid server", func(t *testing.T) {
			h, j := report(`{"server":"UK","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode, "should return 400, got %d", h.StatusCode, "body: %s", j.String())
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("invalid dropType", func(t *testing.T) {
			h, j := report(`{"server":"UK","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"SOMEELSE_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode, "should return 400, got %d", h.StatusCode, "body: %s", j.String())
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("missing required", func(t *testing.T) {
			h, j := report(`{"server":"UK","source":"MeoAssistant","drops":[{"dropType":"SOMEELSE_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode, "should return 400, got %d", h.StatusCode, "body: %s", j.String())
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})
	})

	t.Run("idempotency", func(t *testing.T) {
		idempotencyKey := "backendtest" + uniuri.NewLen(32)

		t.Run("initial request", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, &http.Header{
				"X-Penguin-Idempotency-Key": []string{idempotencyKey},
			})
			assert.Equal(t, http.StatusOK, h.StatusCode, "status code should be 200 but got unexpected value. body: %s", j.String())
			assert.Equal(t, ReportHashLen, len(j.Get("reportHash").String()))
			assert.Equal(t, "saved", h.Header.Get("X-Penguin-Idempotency"), "idempotency key should be saved on initial request")
		})

		t.Run("duplicate request", func(t *testing.T) {
			h, j := report(`very+invalid_body`, &http.Header{
				"X-Penguin-Idempotency-Key": []string{idempotencyKey},
			})
			assert.Equal(t, http.StatusOK, h.StatusCode, "status code should be 200 but got unexpected value. body: %s", j.String())
			assert.Equal(t, ReportHashLen, len(j.Get("reportHash").String()))
			assert.Equal(t, "hit", h.Header.Get("X-Penguin-Idempotency"), "idempotency key should be hit on duplicate request")
		})
	})
}
