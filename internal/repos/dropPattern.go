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
	var patternId int64
	var created bool
	// Yes, I, know. Raw SQL. I spend 7 hours trying to figure out how to do this with bun.
	// This is what I come up with. I'm sorry.
	// This however still is safe enough, since we are using '?' as placeholder. So overall not bad.
	err := tx.QueryRow(`WITH new_row AS (
INSERT INTO drop_patterns (hash)
SELECT
	'?'
WHERE
	NOT EXISTS (
		SELECT
			*
		FROM
			drop_patterns
		WHERE
			hash = '?')
	RETURNING
		pattern_id
)
SELECT
	pattern_id,
	TRUE
FROM
	new_row
UNION
SELECT
	pattern_id,
	FALSE
FROM
	drop_patterns
WHERE
	hash = '?';`, hash).Scan(&patternId)

	if err == sql.ErrNoRows {
		return nil, false, errors.ErrNotFound
	} else if err != nil {
		return nil, false, err
	}

	return &models.DropPattern{
		PatternID: int(patternId),
		Hash:      hash,
	}, created, nil
}
