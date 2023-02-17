package test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dchest/uniuri"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

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
	t.Run("valid body", func(t *testing.T) {
		h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
		assert.Equal(t, http.StatusOK, h.StatusCode, "status code should be 200 but got unexpected value. body: %s", j.String())
		assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
		// TODO: check DB for the report correctness
	})

	t.Run("invalid body", func(t *testing.T) {
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
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"SOMEELSE_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode, "should return 400, got %d", h.StatusCode, "body: %s", j.String())
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("missing required", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","drops":[{"dropType":"SOMEELSE_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
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
