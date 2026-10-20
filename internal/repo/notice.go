package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Notice struct {
	db  *bun.DB
	sel selector.S[model.Notice]
}

func NewNotice(db *bun.DB) *Notice {
	return &Notice{db: db, sel: selector.New[model.Notice](db)}
}

func (r *Notice) GetNotices(ctx context.Context) ([]*model.Notice, error) {
	return r.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("notice_id ASC")
	})
}
