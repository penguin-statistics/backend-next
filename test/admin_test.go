package test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAdminAPI tests the admin API.
func TestAdminAPI(t *testing.T) {
	startup(t)
	t.Parallel()

	t.Run("items resources updated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/recognition/items-resources/updated", bytes.NewBufferString(`{"server":"CN","prefix":"CN/testv0.0.1"}`))
		req.Header.Set("Authorization", "Bearer "+os.Getenv("PENGUIN_V3_ADMIN_KEY"))
		req.Header.Set("Content-Type", "application/json")

		resp := request(t, req)
		assert.Equal(t, http.StatusOK, resp.StatusCode, bodyString(resp))
	})
}
