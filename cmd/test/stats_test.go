package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestV2Stats(t *testing.T) {
	var app *fiber.App
	populate(&app)

	t.Run("GetsSiteStats", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest("GET", "/PenguinStats/api/v2/stats", nil), 5000)
		if err != nil {
			t.Error(err)
		}

		assert.Equal(t, 200, resp.StatusCode, "expect success response")
	})
}
