package repo

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"
	"github.com/zeebo/xxh3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo/selector"
)

// dropPatternsByHashMaxEntries bounds DropPattern.byHash: it is cleared once full.
const dropPatternsByHashMaxEntries = 50000

type DropPattern struct {
	db  *bun.DB
	sel selector.S[model.DropPattern]

	// byHash holds the patterns Remember was given, by hash: a pattern is never changed or deleted once committed. It
	// lives as long as the process: restart it if drop_patterns is ever restored or renumbered.
	byHashMu sync.RWMutex
	byHash   map[string]model.DropPattern
}

func NewDropPattern(db *bun.DB) *DropPattern {
	return &DropPattern{db: db, sel: selector.New[model.DropPattern](db), byHash: map[string]model.DropPattern{}}
}

func (r *DropPattern) GetDropPatterns(ctx context.Context) ([]*model.DropPattern, error) {
	return r.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q
	})
}

func (r *DropPattern) GetDropPatternById(ctx context.Context, id int) (*model.DropPattern, error) {
	return r.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", id)
	})
}

func (r *DropPattern) GetDropPatternByHash(ctx context.Context, hash string) (*model.DropPattern, error) {
	return r.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("hash = ?", hash)
	})
}

// GetOrCreateDropPatternFromDrops returns the pattern of drops, and whether it was created in tx. A pattern remembered
// (see Remember) is returned without querying.
func (r *DropPattern) GetOrCreateDropPatternFromDrops(ctx context.Context, tx bun.Tx, drops []*types.Drop) (*model.DropPattern, bool, error) {
	originalFingerprint, hash := r.calculateDropPatternHash(drops)
	r.byHashMu.RLock()
	cached, ok := r.byHash[hash]
	r.byHashMu.RUnlock()
	if ok {
		return &cached, false, nil
	}

	dropPattern := &model.DropPattern{
		Hash:                hash,
		OriginalFingerprint: originalFingerprint,
	}
	err := tx.NewSelect().
		Model(dropPattern).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == nil {
		// not remembered here: tx may have inserted it, and roll back
		return dropPattern, false, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	_, err = tx.NewInsert().
		Model(dropPattern).
		Exec(ctx)
	if err != nil {
		return nil, false, err
	}

	return dropPattern, true, nil
}

// Remember keeps patterns for GetOrCreateDropPatternFromDrops to return without querying. Only patterns committed may
// be remembered: one selected or inserted in a transaction that rolls back may not exist.
func (r *DropPattern) Remember(patterns ...*model.DropPattern) {
	r.byHashMu.Lock()
	defer r.byHashMu.Unlock()
	for _, pattern := range patterns {
		if len(r.byHash) >= dropPatternsByHashMaxEntries {
			r.byHash = map[string]model.DropPattern{}
		}
		r.byHash[pattern.Hash] = *pattern
	}
}

func (r *DropPattern) calculateDropPatternHash(drops []*types.Drop) (originalFingerprint, hexHash string) {
	segments := make([]string, len(drops))

	for i, drop := range drops {
		segments[i] = fmt.Sprintf("%d:%d", drop.ItemID, drop.Quantity)
	}

	sort.Strings(segments)

	originalFingerprint = strings.Join(segments, "|")
	hash := xxh3.HashStringSeed(originalFingerprint, 0)
	return originalFingerprint, strconv.FormatUint(hash, 16)
}
