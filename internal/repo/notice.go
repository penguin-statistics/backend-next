package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type Notice struct {
	DB *bun.DB
}

func NewNotice(db *bun.DB) *Notice {
	return &Notice{DB: db}
}

func (c *Notice) GetNotices(ctx context.Context) ([]*model.Notice, error) {
	var notice []*model.Notice
	err := c.DB.NewSelect().
		Model(&notice).
		Order("notice_id ASC").
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return notice, nil
}
