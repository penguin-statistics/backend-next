package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type Notice struct {
	DB *bun.DB
}

func NewNotice(db *bun.DB) *Notice {
	return &Notice{DB: db}
}

func (c *Notice) GetNotices(ctx context.Context) ([]*models.Notice, error) {
	var notice []*models.Notice
	err := c.DB.NewSelect().
		Model(&notice).
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return notice, nil
}
