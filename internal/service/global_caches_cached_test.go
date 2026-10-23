package service

import (
	"testing"
	"time"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
)

// The cache sync refreshes the results of a server only in the processes that serve any of those the refresh
// replaces, and drops them in the others: each check sees every key its refresh writes, of that server only, once it
// was read, and each drop deletes every one of them.
func TestGlobalCachesAreCheckedByTheKeysTheirRefreshWrites(t *testing.T) {
	cache.Initialize()
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MatrixWorkerSourceCategories: []string{"all", "automated"}}}
	dropMatrix := &DropMatrix{Config: conf}
	patternMatrix := &PatternMatrix{Config: conf}
	trend := &Trend{}
	siteStats := &SiteStats{}

	for _, tc := range []struct {
		name string
		used func(server string) bool
		drop func(server string)
		set  func(key string)
		read func(key string) error
		has  func(key string) bool
		keys []string
	}{
		{"globalDropMatrix", dropMatrix.GlobalDropMatrixCachesUsed, dropMatrix.DropGlobalDropMatrixCaches,
			func(key string) { cache.GlobalDropMatrix.Set(key, model.DropMatrixQueryResult{}, time.Minute) },
			func(key string) error { var v model.DropMatrixQueryResult; return cache.GlobalDropMatrix.Get(key, &v) },
			cache.GlobalDropMatrix.Has,
			[]string{"KR|all", "KR|automated"}},
		{"shimGlobalDropMatrix", dropMatrix.GlobalDropMatrixCachesUsed, dropMatrix.DropGlobalDropMatrixCaches,
			func(key string) { cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{}, time.Minute) },
			func(key string) error {
				var v modelv2.DropMatrixQueryResult
				return cache.ShimGlobalDropMatrix.Get(key, &v)
			},
			cache.ShimGlobalDropMatrix.Has,
			[]string{"KR|true|all", "KR|false|all", "KR|true|automated", "KR|false|automated"}},
		{"shimGlobalPatternMatrix", patternMatrix.GlobalPatternMatrixCachesUsed, patternMatrix.DropGlobalPatternMatrixCaches,
			func(key string) {
				cache.ShimGlobalPatternMatrix.Set(key, modelv2.PatternMatrixQueryResult{}, time.Minute)
			},
			func(key string) error {
				var v modelv2.PatternMatrixQueryResult
				return cache.ShimGlobalPatternMatrix.Get(key, &v)
			},
			cache.ShimGlobalPatternMatrix.Has,
			[]string{"KR|all|true", "KR|all|false", "KR|automated|true", "KR|automated|false"}},
		{"shimTrend", trend.ShimTrendUsed, trend.DropShimTrend,
			func(key string) { cache.ShimTrend.Set(key, modelv2.TrendQueryResult{}, time.Minute) },
			func(key string) error { var v modelv2.TrendQueryResult; return cache.ShimTrend.Get(key, &v) },
			cache.ShimTrend.Has,
			[]string{"KR"}},
		{"shimSiteStats", siteStats.ShimSiteStatsUsed, siteStats.DropShimSiteStats,
			func(key string) { cache.ShimSiteStats.Set(key, modelv2.SiteStats{}, time.Minute) },
			func(key string) error { var v modelv2.SiteStats; return cache.ShimSiteStats.Get(key, &v) },
			cache.ShimSiteStats.Has,
			[]string{"KR"}},
	} {
		if tc.used("KR") {
			t.Fatalf("%s: KR is used before any key was set", tc.name)
		}
		for _, key := range tc.keys {
			tc.set(key)
			// written only, e.g. by the worker's own refreshes
			if tc.used("KR") {
				t.Errorf("%s: KR is used with %s written, never read", tc.name, key)
			}
			if err := tc.read(key); err != nil {
				t.Fatal(err)
			}
			if !tc.used("KR") {
				t.Errorf("%s: KR is not used with %s read", tc.name, key)
			}
			if tc.used("JP") {
				t.Errorf("%s: JP is used with %s of KR", tc.name, key)
			}
			// another server's drop leaves it
			tc.drop("JP")
			if !tc.has(key) {
				t.Errorf("%s: dropping JP deleted %s", tc.name, key)
			}
			tc.drop("KR")
			if tc.has(key) {
				t.Errorf("%s: dropping KR kept %s", tc.name, key)
			}
		}
	}
}
