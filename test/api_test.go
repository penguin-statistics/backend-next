package test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

func JsonRequestCustom(t *testing.T, req *http.Request, msTimeout ...int) (*http.Response, *gjson.Result) {
	t.Helper()

	resp := request(t, req, msTimeout...)

	bodyBytes, err := io.ReadAll(resp.Body)
	assert.NoError(t, err, "failed to read response body")

	body := gjson.ParseBytes(bodyBytes)

	return resp, &body
}

func JsonRequest(t *testing.T, path, body string, headers *http.Header, msTimeout ...int) (*http.Response, *gjson.Result) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	if headers != nil {
		req.Header = *headers
	}
	req.Header.Set("Content-Type", "application/json")
	return JsonRequestCustom(t, req, msTimeout...)
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

	t.Run("CORS Anonymous Origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/config", nil)
		req.Header.Set("Origin", "https://penguin-stats.io")

		resp := request(
			t,
			req,
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "https://penguin-stats.io", resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	})

	t.Run("CORS Origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/PenguinStats/api/v2/config", nil)
		req.Header.Set("Origin", "https://penguin-stats.io")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Credentials", "true")

		resp := request(t, req)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, "GET,POST,DELETE,OPTIONS", resp.Header.Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "https://penguin-stats.io", resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace", resp.Header.Get("Access-Control-Allow-Headers"))
	})
}
