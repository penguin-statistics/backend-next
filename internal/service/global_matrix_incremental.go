package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uptrace/bun"
	"golang.org/x/sync/singleflight"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

// The incremental global matrices (GlobalMatrixIncremental, #860), what the drop (global_drop_matrix_incremental.go)
// and pattern (global_pattern_matrix_incremental.go) matrices share. The aggregate of a target over every day is the
// aggregate of the days before any day Add that of the others (model.GlobalDropMatrixAggregate.Add,
// model.GlobalPatternMatrixAggregates.Add), so the days before yesterday are kept aggregated, per process, kind,
// server and source category, in a base, and a calculation aggregates only the days since the base, and every day of
// the targets whose time range changed. A base is valid only while no element of its days changed: every write of
// the elements gives the days it touched a new version (scripts/migrations/20261025_matrix_element_day_versions.sql),
// in its own transaction, so a calculation reads the fingerprint of the versions of the base's days and the elements
// since in one snapshot, and extends the base only if the fingerprint is the base's.

// The paths and reasons of observability.GlobalMatrixRefreshPaths.
const (
	globalMatrixPathIncremental = "incremental"
	globalMatrixPathRebuild     = "rebuild"
	globalMatrixPathFull        = "full"

	globalMatrixReasonNone            = "none"
	globalMatrixReasonNoBase          = "no_base"
	globalMatrixReasonFingerprint     = "fingerprint"
	globalMatrixReasonDayBack         = "day_back"
	globalMatrixReasonMaxAge          = "max_age"
	globalMatrixReasonChangedStages   = "changed_stages"
	globalMatrixReasonTriggersMissing = "triggers_missing"

	globalMatrixKindDrop    = "drop"
	globalMatrixKindPattern = "pattern"

	// maxLoggedMismatches bounds the keys a verification logs.
	maxLoggedMismatches = 20
)

// GlobalMatrixFingerprints reads, in the snapshot of db, what the bases of the incremental global matrices are
// checked against. The default reads the day versions of the elements; a benchmark on a database without them uses a
// constant one.
type GlobalMatrixFingerprints interface {
	// Triggers returns what identifies the triggers that version the days of the elements of table, or why the day
	// versions cannot be relied on (missing is not empty): a trigger is missing or not firing always, or the versions
	// cannot be read.
	Triggers(ctx context.Context, db bun.IDB, table string) (identity, missing string, err error)
	// DayFingerprints returns, for each of bounds, the fingerprint of the day versions of the elements of server in
	// tbl (drop or pattern) of the days before it.
	DayFingerprints(ctx context.Context, db bun.IDB, tbl, server string, bounds []int) (map[int]string, error)
}

// dayVersionFingerprints reads the day versions the triggers keep.
type dayVersionFingerprints struct{}

func (dayVersionFingerprints) Triggers(ctx context.Context, db bun.IDB, table string) (identity, missing string, err error) {
	enabled, identity, readable, err := repo.ReadMatrixElementDayVersionTriggers(ctx, db, table)
	if err != nil {
		return "", "", err
	}
	if enabled != repo.MatrixElementDayVersionTriggers {
		return identity, fmt.Sprintf("%d of the %d triggers of the day versions of %s fire always", enabled, repo.MatrixElementDayVersionTriggers, table), nil
	}
	if !readable {
		return identity, "the day versions cannot be read (matrix_element_day_versions is missing, or SELECT is not granted)", nil
	}
	return identity, "", nil
}

func (dayVersionFingerprints) DayFingerprints(ctx context.Context, db bun.IDB, tbl, server string, bounds []int) (map[int]string, error) {
	return repo.ReadMatrixElementDayFingerprints(ctx, db, tbl, server, bounds)
}

// globalMatrixIncremental is the state of the incremental global matrices of a kind in a process; the zero value is
// ready.
type globalMatrixIncremental struct {
	// jitter is the fraction of GlobalMatrixBaseMaxAge taken off it in this process, in [0, 0.1)
	jitterOnce sync.Once
	jitter     float64
	// calculations counts the calculations of each server (*atomic.Int64), for GlobalMatrixVerifyEvery
	calculations sync.Map
	// rebuilds runs a rebuild of a server and source category once for the callers that need it at once (see
	// shareRebuild)
	rebuilds singleflight.Group
}

func (g *globalMatrixIncremental) maxAge(configured time.Duration) time.Duration {
	if configured <= 0 {
		configured = 24 * time.Hour
	}
	g.jitterOnce.Do(func() { g.jitter = rand.Float64() / 10 })
	return configured - time.Duration(float64(configured)*g.jitter)
}

// verifies counts a calculation of server, and reports whether it is verified.
func (g *globalMatrixIncremental) verifies(conf *appconfig.Config, server string) bool {
	if conf.GlobalMatrixIncremental == appconfig.GlobalMatrixIncrementalVerify {
		return true
	}
	if conf.GlobalMatrixVerifyEvery <= 0 {
		return false
	}
	counter, _ := g.calculations.LoadOrStore(server, &atomic.Int64{})
	return counter.(*atomic.Int64).Add(1)%int64(conf.GlobalMatrixVerifyEvery) == 0
}

// shareRebuild runs rebuild, the rebuild of the base of key (a server and source category), or waits for that of
// another caller already running and returns its results (shared), if usable says they are for this caller: nil if
// this caller rebuilt. A refresh does not wait for a rebuild running: the snapshot of a rebuild that started before
// the refresh may be of before the batch the refresh publishes, and nothing would then keep the refresh from caching
// it over newer matrices (cache.Set.Set does not check), so a refresh starts a rebuild of its own, which the
// calculations that need one after it share. A request may take the results of any rebuild: if a refresh wrote the
// matrices of the request meanwhile, cache.Set.MutexGetSet does not cache the request's results over them. A caller
// that waited for a rebuild that failed because the context of the caller running it was done (canceled, past its
// deadline) rebuilds itself, unless its own context is done too.
func (g *globalMatrixIncremental) shareRebuild(
	ctx context.Context, key string, refresh bool, rebuild func() (interface{}, error), usable func(shared interface{}) bool,
) (shared interface{}, err error) {
	if refresh {
		// a rebuild running is left to its callers; the rebuilds that start from now on are after the refresh
		g.rebuilds.Forget(key)
	}
	var executed bool
	v, err, _ := g.rebuilds.Do(key, func() (interface{}, error) {
		executed = true
		return rebuild()
	})
	if executed { // this caller rebuilt
		return nil, err
	}
	if err != nil {
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() == nil {
			_, err = rebuild()
		}
		return nil, err
	}
	if !usable(v) {
		_, err := rebuild()
		return nil, err
	}
	return v, nil
}

// sameGlobalMatrixTargetRanges reports whether a and b have the same targets, with the same time ranges.
func sameGlobalMatrixTargetRanges[K comparable](a, b map[K]model.GlobalMatrixSecondRange) bool {
	if len(a) != len(b) {
		return false
	}
	for key, r := range a {
		if other, ok := b[key]; !ok || other != r {
			return false
		}
	}
	return true
}

func formatNullInt(i null.Int) string {
	if !i.Valid {
		return "null"
	}
	return fmt.Sprint(i.Int64)
}
