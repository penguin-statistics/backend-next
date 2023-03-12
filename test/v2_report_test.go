package test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dchest/uniuri"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestAPIV2Reports(t *testing.T) {
	startup(t)
	t.Parallel()

	// helpers
	jsonReqCustom := func(req *http.Request) (*http.Response, *gjson.Result) {
		t.Helper()

		resp := request(t, req)

		bodyBytes, err := io.ReadAll(resp.Body)
		assert.NoError(t, err, "failed to read response body")

		body := gjson.ParseBytes(bodyBytes)

		return resp, &body
	}

	jsonReq := func(path, body string, headers *http.Header) (*http.Response, *gjson.Result) {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		if headers != nil {
			req.Header = *headers
		}
		req.Header.Set("Content-Type", "application/json")
		return jsonReqCustom(req)
	}

	report := func(body string, headers *http.Header) (*http.Response, *gjson.Result) {
		t.Helper()
		return jsonReq("/PenguinStats/api/v2/report", body, headers)
	}

	recall := func(body string, headers *http.Header) (*http.Response, *gjson.Result) {
		t.Helper()
		return jsonReq("/PenguinStats/api/v2/report/recall", body, headers)
	}

	// tests
	t.Run("valid body", func(t *testing.T) {
		t.Run("basic", func(t *testing.T) {
			h, j := report(ReportValidBody, nil)
			assert.Equal(t, http.StatusOK, h.StatusCode)
			assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
			assert.NotEmpty(t, h.Header.Get("X-Penguin-Set-PenguinID"))
			// TODO: check DB for the report correctness
		})
	})

	t.Run("valid account", func(t *testing.T) {
		var accountId string
		{
			h, j := report(ReportValidBody, nil)
			assert.Equal(t, http.StatusOK, h.StatusCode)
			assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
			accountId = h.Header.Get("X-Penguin-Set-PenguinID")
			assert.NotEmpty(t, accountId)
		}
		{
			h, j := report(ReportValidBody, &http.Header{
				"Authorization": []string{"PenguinID " + accountId},
			})
			assert.Equal(t, http.StatusOK, h.StatusCode)
			assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
			assert.Empty(t, h.Header.Get("X-Penguin-Set-PenguinID"), "should not create account when a valid one is provided")
		}
	})

	t.Run("invalid account", func(t *testing.T) {
		h, j := report(ReportValidBody, &http.Header{
			"Authorization": []string{"PenguinID 1145141919810"},
		})
		assert.Equal(t, http.StatusOK, h.StatusCode)
		assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
		assert.NotEmpty(t, h.Header.Get("X-Penguin-Set-PenguinID"), "should create account when an invalid is provided")
	})

	t.Run("invalid body", func(t *testing.T) {
		t.Run("invalid json", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode)
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("invalid server", func(t *testing.T) {
			h, j := report(`{"server":"UK","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode)
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("invalid dropType", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"SOMEELSE_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode)
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("missing required", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","drops":[],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode)
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})

		t.Run("negative quantity", func(t *testing.T) {
			h, j := report(`{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2003","quantity":-1}],"version":"v3.0.4"}`, nil)
			assert.Equal(t, http.StatusBadRequest, h.StatusCode)
			assert.NotEmpty(t, j.Get("message").String(), "error message should not be empty")
		})
	})

	t.Run("idempotency", func(t *testing.T) {
		idempotencyKey := "backendtest" + uniuri.NewLen(32)

		t.Run("initial request", func(t *testing.T) {
			h, j := report(ReportValidBody, &http.Header{
				"X-Penguin-Idempotency-Key": []string{idempotencyKey},
			})
			assert.Equal(t, http.StatusOK, h.StatusCode)
			assert.Equal(t, ReportHashLen, len(j.Get("reportHash").String()))
			assert.Equal(t, "saved", h.Header.Get("X-Penguin-Idempotency"), "idempotency key should be saved on initial request")
		})

		t.Run("duplicate request", func(t *testing.T) {
			h, j := report(`very+invalid_body`, &http.Header{
				"X-Penguin-Idempotency-Key": []string{idempotencyKey},
			})
			assert.Equal(t, http.StatusOK, h.StatusCode)
			assert.Equal(t, ReportHashLen, len(j.Get("reportHash").String()))
			assert.Equal(t, "hit", h.Header.Get("X-Penguin-Idempotency"), "idempotency key should be hit on duplicate request")
		})
	})

	t.Run("recall", func(t *testing.T) {
		t.Run("basic", func(t *testing.T) {
			var reportHash string
			{
				h, j := report(ReportValidBody, nil)
				assert.Equal(t, http.StatusOK, h.StatusCode)
				assert.Equal(t, len(j.Get("reportHash").String()), ReportHashLen)
				reportHash = j.Get("reportHash").String()
			}
			time.Sleep(time.Second * 2) // FIXME: wait for the report to be consumed
			{
				h, j := recall(`{"reportHash":"`+reportHash+`"}`, nil)
				assert.Equal(t, http.StatusOK, h.StatusCode, j.String())
			}
		})
	})
}
