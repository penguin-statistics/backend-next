package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"

	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/testentry"
)

func TestV2Report(t *testing.T) {
	var app *fiber.App
	testentry.Populate(t, &app)

	t.Run("ReportSuccessfully", func(t *testing.T) {
		s := bytes.NewBufferString(`{"drops":[{"dropType":"NORMAL_DROP","itemId":"30012","quantity":1},{"dropType":"EXTRA_DROP","itemId":"30021","quantity":1},{"dropType":"EXTRA_DROP","itemId":"2001","quantity":1}],"stageId":"main_01-07","server":"CN","source":"frontend-v2-localhost-testing","version":"v3.0.0"}`)
		req := httptest.NewRequest("POST", "/PenguinStats/api/v2/report", s)
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, 500)

		assert.NoError(t, err, "expect success response")
		assert.Equal(t, 200, resp.StatusCode, "expect success response")
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"), "expect json response")

		var response modelv2.ReportResponse
		b, err := io.ReadAll(resp.Body)
		assert.NoError(t, err, "expect no error when reading response body")
		assert.NoError(t, json.Unmarshal(b, &response), "expect no error when unmarshalling response body")
		assert.Regexp(t, "^[0-9a-z]{20}-[0-9a-zA-Z]{16}$", response.ReportHash, "expect report hash to be an expected format")
	})
}
