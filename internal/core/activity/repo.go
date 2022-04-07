package activity

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

type Repo struct {
	DB *bun.DB
}

func NewRepo(db *bun.DB) *Repo {
	return &Repo{DB: db}
}

func (c *Repo) GetActivities(ctx context.Context) ([]*Model, error) {
	var activities []*Model
	err := c.DB.NewSelect().
		Model(&activities).
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return activities, nil
}
