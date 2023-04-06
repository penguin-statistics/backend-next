package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestAPIV2AdvancedQuery(t *testing.T) {
	startup(t)
	t.Parallel()

	query := func(body string, headers *http.Header) (*http.Response, *gjson.Result) {
		t.Helper()
		return JsonRequest(t, "/PenguinStats/api/v2/result/advanced", body, headers)
	}

	t.Run("valid body", func(t *testing.T) {
		h, j := query(`{"queries":[{"stageId":"main_01-07","itemIds":[],"server":"CN","isPersonal":false,"sourceCategory":"all","start":1556668800000,"end":1562630400000}]}`, nil)
		assert.Equal(t, http.StatusOK, h.StatusCode)
		assert.NotEmpty(t, len(j.Get("advanced_results").String()))
	})

	t.Run("invalid body", func(t *testing.T) {
		h, _ := query(`{"queries":[{"stageId":"main_01-07","itemIds":[],"server":"CN","isPersonal":false,"sourceCategory":"all","start":1556668800000,"end":1556668800000}]}`, nil)
		assert.Equal(t, http.StatusBadRequest, h.StatusCode)
	})
}
