package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"

	"github.com/penguin-statistics/backend-next/internal/pkg/testentry"
)

func BenchmarkV2Stages(b *testing.B) {
	b.Skip("enable when needed")
	var app *fiber.App
	testentry.Populate(b, &app)

	b.Run("GetsV2StagesWithRedisCache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resp, err := app.Test(httptest.NewRequest("GET", "/api/v3/stages/main_00-01", nil))

			assert.NoError(b, err, "expect success response")
			assert.Equal(b, 200, resp.StatusCode, "expect success response")
		}
	})
}
