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

func JsonRequestCustom(t *testing.T, req *http.Request) (*http.Response, *gjson.Result) {
	t.Helper()

	resp := request(t, req)

	bodyBytes, err := io.ReadAll(resp.Body)
	assert.NoError(t, err, "failed to read response body")

	body := gjson.ParseBytes(bodyBytes)

	return resp, &body
}

func JsonRequest(t *testing.T, path, body string, headers *http.Header) (*http.Response, *gjson.Result) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	if headers != nil {
		req.Header = *headers
	}
	req.Header.Set("Content-Type", "application/json")
	return JsonRequestCustom(t, req)
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
