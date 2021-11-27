package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestIndex(t *testing.T) {
	var app *fiber.App
	populate(&app)

	t.Run("GetsCorrectIndex", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Error(err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("Expected status code to be 200, got %d", resp.StatusCode)
		}
	})
}
