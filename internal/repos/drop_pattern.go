package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type DropPatternRepo struct {
	DB *bun.DB
}

func NewDropPatternRepo(db *bun.DB) *DropPatternRepo {
	return &DropPatternRepo{DB: db}
}

func (s *DropPatternRepo) GetDropPatternById(ctx context.Context, id int) (*models.DropPattern, error) {
	var dropPattern models.DropPattern
	err := s.DB.NewSelect().
		Model(&dropPattern).
		Where("id = ?", id).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropPattern, nil
}

func (s *DropPatternRepo) GetDropPatternByHash(ctx context.Context, hash string) (*models.DropPattern, error) {
	var dropPattern models.DropPattern
	err := s.DB.NewSelect().
		Model(&dropPattern).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropPattern, nil
}

func (s *DropPatternRepo) GetOrCreateDropPatternByHash(ctx context.Context, tx bun.Tx, hash string) (*models.DropPattern, bool, error) {
	dropPattern := &models.DropPattern{
		Hash: hash,
	}
	err := tx.NewSelect().
		Model(dropPattern).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == nil {
		return dropPattern, false, nil
	} else if err != nil && err != sql.ErrNoRows {
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
