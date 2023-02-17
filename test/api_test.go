package test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
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
