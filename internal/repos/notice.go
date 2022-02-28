package repos

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type NoticeRepo struct {
	DB *bun.DB
}

func NewNoticeRepo(db *bun.DB) *NoticeRepo {
	return &NoticeRepo{DB: db}
}

func (c *NoticeRepo) GetNotices(ctx context.Context) ([]*models.Notice, error) {
	var notice []*models.Notice
	err := c.DB.NewSelect().
		Model(&notice).
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return notice, nil
}
