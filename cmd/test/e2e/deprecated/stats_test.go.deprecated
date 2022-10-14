package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"

	"github.com/penguin-statistics/backend-next/internal/pkg/testentry"
)

func TestV2Stats(t *testing.T) {
	var app *fiber.App
	testentry.Populate(t, &app)

	t.Run("GetsSiteStats", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest("GET", "/PenguinStats/api/v2/stats", nil), 5000)

		assert.NoError(t, err, "expect success response")
		assert.Equal(t, 200, resp.StatusCode, "expect success response")
	})
}
