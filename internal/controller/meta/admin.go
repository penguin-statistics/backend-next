package meta

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/zeebo/xxh3"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/model/gamedata"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type AdminController struct {
	fx.In

	PatternRepo          *repo.DropPattern
	PatternElementRepo   *repo.DropPatternElement
	AdminService         *service.Admin
	ItemService          *service.Item
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
	AnalyticsService     *service.Analytics
}

func RegisterAdmin(admin *svr.Admin, c AdminController) {
	admin.Get("/bonjour", c.Bonjour)
	admin.Post("/save", c.SaveRenderedObjects)
	admin.Post("/purge", c.PurgeCache)

	admin.Get("/cli/gamedata/seed", c.GetCliGameDataSeed)
	admin.Get("/_temp/pattern/merging", c.FindPatterns)
	admin.Get("/_temp/pattern/disambiguation", c.DisambiguatePatterns)

	admin.Get("/analytics/report-unique-users/by-source", c.GetRecentUniqueUserCountBySource)

	admin.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	admin.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	admin.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	admin.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)
}

type CliGameDataSeedResponse struct {
	Items []*model.Item `json:"items"`
}

// Bonjour is for the admin dashboard to detect authentication status
func (c AdminController) Bonjour(ctx *fiber.Ctx) error {
	return ctx.SendStatus(http.StatusNoContent)
}

func (c AdminController) FindPatterns(ctx *fiber.Ctx) error {
	patterns, err := c.PatternRepo.GetDropPatterns(ctx.UserContext())
	if err != nil {
		return err
	}

	var sb strings.Builder

	for _, pattern := range patterns {
		m := map[string]int{}
		haveDup := false
		segments := strings.Split(pattern.OriginalFingerprint, "|")
		if len(segments) == 1 && segments[0] == "" {
			continue
		}
		for _, segment := range segments {
			a := strings.Split(segment, ":")
			if _, ok := m[a[0]]; ok {
				haveDup = true
			}

			i, _ := strconv.Atoi(a[1])

			if _, ok := m[a[0]]; ok {
				m[a[0]] = m[a[0]] + i
			} else {
				m[a[0]] = i
			}
		}
		if haveDup {
			segments := make([]string, 0)

			for i, j := range m {
				segments = append(segments, fmt.Sprintf("%s:%d", i, j))
			}

			fingerprint, hash := c.calculateDropPatternHash(segments)

			correctPattern, err := c.PatternRepo.GetDropPatternByHash(ctx.UserContext(), hash)
			if err != nil {
				spew.Dump(hash, fingerprint, err)
				sb.WriteString(fmt.Sprintf(`WITH inserted_id AS (INSERT INTO drop_patterns ("hash", "original_fingerprint") VALUES ('%s', '%s') RETURNING pattern_id)
UPDATE drop_reports SET pattern_id = (select pattern_id from inserted_id) WHERE pattern_id = '%d';
`, hash, fingerprint, pattern.PatternID))
				continue
			}

			sb.WriteString(fmt.Sprintf("UPDATE drop_reports SET pattern_id = '%d' WHERE pattern_id = '%d';\n", correctPattern.PatternID, pattern.PatternID))
		}
	}

	return ctx.SendString(sb.String())
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (c AdminController) DisambiguatePatterns(ctx *fiber.Ctx) error {
	patterns, err := c.PatternRepo.GetDropPatterns(ctx.UserContext())
	if err != nil {
		return err
	}

	elements, err := c.PatternElementRepo.GetDropPatternElements(ctx.UserContext())
	if err != nil {
		return err
	}

	getElementsByPatternId := func(patternId int) []*model.DropPatternElement {
		var filtered []*model.DropPatternElement
		for _, element := range elements {
			if element.DropPatternID == patternId {
				filtered = append(filtered, element)
			}
		}
		return filtered
	}

	var sb strings.Builder
	sb.WriteString("BEGIN\n")

	for _, pattern := range patterns {
		segments := strings.Split(pattern.OriginalFingerprint, "|")
		if len(segments) == 1 && segments[0] == "" {
			continue
		}
		storedPatterns := getElementsByPatternId(pattern.PatternID)
		calculatedPatterns := make([]*model.DropPatternElement, 0)
		for _, segment := range segments {
			a := strings.Split(segment, ":")
			itemId := must(strconv.Atoi(a[0]))
			quantity := must(strconv.Atoi(a[1]))
			calculatedPatterns = append(calculatedPatterns, &model.DropPatternElement{
				DropPatternID: pattern.PatternID,
				ItemID:        itemId,
				Quantity:      quantity,
			})
		}
		// // compare calculated and stored patterns
		// if len(storedPatterns) != len(calculatedPatterns) {
		// 	sb.WriteString(fmt.Sprintf("LENGTH: (patternId: %d) %d != %d\n", pattern.PatternID, len(storedPatterns), len(calculatedPatterns)))
		// 	continue
		// } else {

		// }
		storedPatternsOF, storedPatternsHash := c.calculateDropPatternHashFromElements(storedPatterns)
		calculatedPatternsOF, calculatedPatternsHash := c.calculateDropPatternHashFromElements(calculatedPatterns)
		if storedPatternsHash != calculatedPatternsHash {
			sb.WriteString(fmt.Sprintf("HASH: (patternId: %d) %s (%s) != %s (%s)\n", pattern.PatternID, storedPatternsHash, storedPatternsOF, calculatedPatternsHash, calculatedPatternsOF))
			continue
		}
	}

	sb.WriteString("END\n")

	return ctx.SendString(sb.String())
}

func (c AdminController) calculateDropPatternHashFromElements(elements []*model.DropPatternElement) (originalFingerprint, hexHash string) {
	segments := make([]string, len(elements))

	for i, element := range elements {
		segments[i] = fmt.Sprintf("%d:%d", element.ItemID, element.Quantity)
	}

	sort.Strings(segments)

	originalFingerprint = strings.Join(segments, "|")
	hash := xxh3.HashStringSeed(originalFingerprint, 0)
	return originalFingerprint, strconv.FormatUint(hash, 16)
}

func (c AdminController) calculateDropPatternHash(segments []string) (originalFingerprint, hexHash string) {
	sort.Strings(segments)

	originalFingerprint = strings.Join(segments, "|")
	hash := xxh3.HashStringSeed(originalFingerprint, 0)
	return originalFingerprint, strconv.FormatUint(hash, 16)
}

func (c AdminController) GetCliGameDataSeed(ctx *fiber.Ctx) error {
	items, err := c.ItemService.GetItems(ctx.UserContext())
	if err != nil {
		return err
	}

	return ctx.JSON(CliGameDataSeedResponse{
		Items: items,
	})
}

func (c *AdminController) SaveRenderedObjects(ctx *fiber.Ctx) error {
	var request gamedata.RenderedObjects
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}

	err := c.AdminService.SaveRenderedObjects(ctx.UserContext(), &request)
	if err != nil {
		return err
	}

	return ctx.JSON(request)
}

func (c *AdminController) PurgeCache(ctx *fiber.Ctx) error {
	var request types.PurgeCacheRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	errs := lo.Filter(
		lo.Map(request.Pairs, func(pair types.PurgeCachePair, _ int) error {
			err := cache.Delete(pair.Name, pair.Key)
			if err != nil {
				return errors.Wrapf(err, "cache [%s:%s]", pair.Name, pair.Key)
			}
			return nil
		}),
		func(v error, i int) bool {
			return v != nil
		},
	)
	if len(errs) > 0 {
		err := pgerr.New(http.StatusInternalServerError, "PURGE_CACHE_FAILED", "error occurred while purging cache")
		err.Extras = &pgerr.Extras{
			"caches": errs,
		}
	}
	return nil
}

func (c *AdminController) GetRecentUniqueUserCountBySource(ctx *fiber.Ctx) error {
	recent := ctx.Query("recent", constant.DefaultRecentDuration)
	result, err := c.AnalyticsService.GetRecentUniqueUserCountBySource(ctx.UserContext(), recent)
	if err != nil {
		return err
	}
	return ctx.JSON(result)
}

func (c *AdminController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx.UserContext(), server, []string{constant.SourceCategoryAll})
}

func (c *AdminController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx.UserContext(), server, []string{constant.SourceCategoryAll})
}

func (c *AdminController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx.UserContext(), server, []string{constant.SourceCategoryAll})
}

func (c *AdminController) RefreshAllSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	_, err := c.SiteStatsService.RefreshShimSiteStats(ctx.UserContext(), server)
	return err
}
