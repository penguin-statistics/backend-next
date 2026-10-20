package v2

// An integration test of the matrix handlers on a real PostgreSQL, see integration_test.go.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

// The personal drop and pattern matrices hold the results of the account in the Authorization header, so no shared
// cache may keep them, while the global ones are cacheable.
func TestPersonalMatricesAreNotCacheable(t *testing.T) {
	db := newTestDB(t)
	flushCaches(t)
	t.Cleanup(func() { flushCaches(t) })

	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MatrixWorkerSourceCategories: []string{"all"}, PatternMatrixLimit: 19}}
	timeRangeService := service.NewTimeRange(repo.NewTimeRange(db), repo.NewDropInfo(db))
	dropReportService := service.NewDropReport(repo.NewDropReport(db))
	dropInfoService := service.NewDropInfo(repo.NewDropInfo(db), timeRangeService)
	stageService := service.NewStage(repo.NewStage(db))
	itemService := service.NewItem(repo.NewItem(db))
	dropMatrixService := service.NewDropMatrix(conf, timeRangeService, dropReportService, dropInfoService,
		service.NewDropMatrixElement(repo.NewDropMatrixElement(db)), stageService, itemService, nil)
	patternMatrixService := service.NewPatternMatrix(conf, timeRangeService, dropReportService, dropInfoService,
		service.NewPatternMatrixElement(repo.NewPatternMatrixElement(db)), service.NewDropPatternElement(repo.NewDropPatternElement(db)),
		stageService, itemService)
	accountService := service.NewAccount(repo.NewAccount(db))

	app := fiber.New()
	app.Use(middlewares.InjectI18n()) // for the validation of the query
	v2 := &svr.V2{Router: app.Group("/PenguinStats/api/v2")}
	RegisterResult(v2, Result{DropMatrixService: dropMatrixService, PatternMatrixService: patternMatrixService, AccountService: accountService})
	RegisterPrivate(v2, Private{DropMatrixService: dropMatrixService, PatternMatrixService: patternMatrixService, AccountService: accountService})

	notCacheable := "no-cache, no-store, must-revalidate"
	tests := []struct {
		path string
		want string
	}{
		{"/result/matrix?server=CN", "public, max-age=300"},
		{"/result/matrix?server=CN&is_personal=true", notCacheable},
		{"/result/pattern?server=CN", "public, max-age=300"},
		{"/result/pattern?server=CN&is_personal=true", notCacheable},
		{"/_private/result/matrix/CN/global", "public, max-age=300"},
		{"/_private/result/matrix/CN/personal", notCacheable},
		{"/_private/result/pattern/CN/global", "public, max-age=300"},
		{"/_private/result/pattern/CN/personal", notCacheable},
	}
	// the headers of the second request are checked, by which the global matrices have been calculated and cached
	get := func(path string) *http.Response {
		req := httptest.NewRequest(fiber.MethodGet, "/PenguinStats/api/v2"+path, nil)
		req.Header.Set(fiber.HeaderAuthorization, "PenguinID 11111111")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		return resp
	}
	for _, tt := range tests {
		get(tt.path)
		if got := get(tt.path).Header.Get(fiber.HeaderCacheControl); got != tt.want {
			t.Errorf("%s: Cache-Control %q, want %q", tt.path, got, tt.want)
		}
	}
}
