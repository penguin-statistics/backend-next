package test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAPIV2Resources tests the resources API except the reports API.
func TestAPIV2Resources(t *testing.T) {
	startup(t)
	t.Parallel()

	t.Run("items", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/items", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stages", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/stages", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("zones", func(t *testing.T) {
		resp := request(
			t,
			httptest.NewRequest(http.MethodGet, "/PenguinStats/api/v2/zones", nil),
		)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
