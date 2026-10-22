package repo

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
)

// queryCounter counts the statements run through a bun.DB.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}
func (c *queryCounter) AfterQuery(context.Context, *bun.QueryEvent) { c.n.Add(1) }

// TestGetOrCreateDropPatternReturnsRememberedPatterns runs against PENGUIN_TEST_POSTGRES_DSN (see integration_test.go).
func TestGetOrCreateDropPatternReturnsRememberedPatterns(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE drop_patterns (
		pattern_id bigserial PRIMARY KEY,
		hash text NOT NULL UNIQUE,
		original_fingerprint text NOT NULL
	)`)
	counter := &queryCounter{}
	db.AddQueryHook(counter)
	r := NewDropPattern(db)

	// getOrCreate calls GetOrCreateDropPatternFromDrops in a transaction it commits, or rolls back if rollback is set,
	// and returns the number of statements it ran besides BEGIN and COMMIT or ROLLBACK
	getOrCreate := func(drops []*types.Drop, rollback bool) (*model.DropPattern, bool, int64) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		before := counter.n.Load()
		pattern, created, err := r.GetOrCreateDropPatternFromDrops(ctx, tx, drops)
		if err != nil {
			t.Fatal(err)
		}
		statements := counter.n.Load() - before
		if rollback {
			err = tx.Rollback()
		} else {
			err = tx.Commit()
		}
		if err != nil {
			t.Fatal(err)
		}
		return pattern, created, statements
	}

	drops := []*types.Drop{{ItemID: 30012, Quantity: 2}, {ItemID: 30011, Quantity: 1}}
	mustExec(t, db, "INSERT INTO drop_patterns (hash, original_fingerprint) VALUES ('other', 'other')")
	fingerprint, hash := r.calculateDropPatternHash(drops)
	// the fingerprint in the database is what the pattern returns, not the one calculated
	mustExec(t, db, "INSERT INTO drop_patterns (hash, original_fingerprint) VALUES (?, ?)", hash, fingerprint+" (stored)")

	first, wasCreated, statements := getOrCreate(drops, false)
	if wasCreated || statements != 1 {
		t.Fatalf("first call: created %v with %d statements, want an existing pattern selected", wasCreated, statements)
	}
	want := model.DropPattern{PatternID: 2, Hash: hash, OriginalFingerprint: fingerprint + " (stored)"}
	if *first != want {
		t.Fatalf("first call returned %+v, want %+v", *first, want)
	}
	// a pattern selected is not remembered until Remember is given it
	if _, _, statements := getOrCreate(drops, false); statements != 1 {
		t.Fatalf("second call: %d statements, want the pattern selected again", statements)
	}
	r.Remember(first)
	second, wasCreated, statements := getOrCreate(drops, false)
	if wasCreated || statements != 0 || *second != want || second == first {
		t.Fatalf("remembered: %+v (created %v) with %d statements, want a copy of %+v with none", *second, wasCreated, statements, want)
	}

	// a pattern inserted and selected again in a transaction that rolls back is not remembered: it does not exist
	newDrops := []*types.Drop{{ItemID: 30013, Quantity: 1}}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, wasCreated, err := r.GetOrCreateDropPatternFromDrops(ctx, tx, newDrops); err != nil || !wasCreated {
		t.Fatalf("new pattern: created %v, %v", wasCreated, err)
	}
	if _, wasCreated, err := r.GetOrCreateDropPatternFromDrops(ctx, tx, newDrops); err != nil || wasCreated {
		t.Fatalf("new pattern again in its transaction: created %v, %v, want it selected", wasCreated, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	created, wasCreated, statements := getOrCreate(newDrops, false)
	if !wasCreated || statements != 2 {
		t.Fatalf("rolled back pattern: created %v with %d statements, want it selected and inserted", wasCreated, statements)
	}
	r.Remember(created)
	if again, wasCreated, statements := getOrCreate(newDrops, false); wasCreated || statements != 0 || *again != *created {
		t.Fatalf("remembered pattern created: %+v (created %v) with %d statements, want %+v with none", *again, wasCreated, statements, *created)
	}

	// once full, the cache is cleared before the next pattern is remembered
	r.byHashMu.Lock()
	r.byHash = map[string]model.DropPattern{}
	for i := 0; i < dropPatternsByHashMaxEntries; i++ {
		r.byHash[strconv.Itoa(i)] = model.DropPattern{}
	}
	r.byHashMu.Unlock()
	r.Remember(first)
	r.byHashMu.RLock()
	n := len(r.byHash)
	r.byHashMu.RUnlock()
	if n != 1 {
		t.Fatalf("the cache holds %d patterns after it was full, want 1", n)
	}
	if _, _, statements := getOrCreate(drops, false); statements != 0 {
		t.Fatalf("remembered after the cache was cleared: %d statements, want none", statements)
	}
}
