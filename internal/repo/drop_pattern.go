package repo

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"
	"github.com/zeebo/xxh3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type DropPattern struct {
	db  *bun.DB
	sel selector.S[model.DropPattern]
}

func NewDropPattern(db *bun.DB) *DropPattern {
	return &DropPattern{db: db, sel: selector.New[model.DropPattern](db)}
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

func (r *DropPattern) GetOrCreateDropPatternFromDrops(ctx context.Context, tx bun.Tx, drops []*types.Drop) (*model.DropPattern, bool, error) {
	originalFingerprint, hash := r.calculateDropPatternHash(drops)
	dropPattern := &model.DropPattern{
		Hash:                hash,
		OriginalFingerprint: originalFingerprint,
	}
	err := tx.NewSelect().
		Model(dropPattern).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == nil {
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
