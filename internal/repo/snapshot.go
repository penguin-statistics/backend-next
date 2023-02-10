package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Snapshot struct {
	db *bun.DB

	sel selector.S[model.Snapshot]
}

func NewSnapshot(db *bun.DB) *Snapshot {
	return &Snapshot{
		db:  db,
		sel: selector.New[model.Snapshot](db),
	}
}

func (s *Snapshot) GetSnapshotById(ctx context.Context, id int) (*model.Snapshot, error) {
	return s.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", id)
	})
}

func (s *Snapshot) GetSnapshotsByIds(ctx context.Context, ids []int) ([]*model.Snapshot, error) {
	return s.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id IN (?)", ids)
	})
}

func (s *Snapshot) GetLatestSnapshotByKey(ctx context.Context, key string) (*model.Snapshot, error) {
	return s.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("key = ?", key).OrderExpr("snapshot_id DESC").Limit(1)
	})
}

func (s *Snapshot) GetSnapshotsByVersions(ctx context.Context, key string, versions []string) ([]*model.Snapshot, error) {
	return s.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("key = ?", key).Where("version IN (?)", bun.In(versions))
	})
}

func (s *Snapshot) SaveSnapshot(ctx context.Context, snapshot *model.Snapshot) (*model.Snapshot, error) {
	_, err := s.db.NewInsert().
		Model(snapshot).
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	return snapshot, err
}
