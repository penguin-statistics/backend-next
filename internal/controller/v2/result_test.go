package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
)

// the global drop matrices are served from the cache, which the tests fill instead of a database
func TestGetDropMatrixLastModifiedOfCategory(t *testing.T) {
	cache.Initialize()
	t.Cleanup(func() {
		cache.ShimGlobalDropMatrix.Flush()
		cache.LastModifiedTime.Flush()
	})
	lastModified := map[string]time.Time{
		constant.SourceCategoryAll:    time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		constant.SourceCategoryManual: time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC),
	}
	for category, modified := range lastModified {
		key := "CN" + constant.CacheSep + "false" + constant.CacheSep + category
		cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: category}}}, time.Hour)
		cache.LastModifiedTime.Set("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", modified, 0)
	}

	app := fiber.New()
	app.Use(middlewares.InjectI18n()) // for the validation of the query
	c := &Result{DropMatrixService: &service.DropMatrix{}}
	app.Get("/result/matrix", c.GetDropMatrix)
	for category, modified := range lastModified {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/result/matrix?server=CN&category="+category, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: status %d", category, resp.StatusCode)
		}
		if got, want := resp.Header.Get(fiber.HeaderLastModified), modified.Format(http.TimeFormat); got != want {
			t.Errorf("%s: Last-Modified %q, want %q", category, got, want)
		}
	}
}
