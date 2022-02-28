package repos

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

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
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

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
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

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropPattern, nil
}

func (s *DropPatternRepo) GetOrCreateDropPatternFromDrops(ctx context.Context, tx bun.Tx, drops []*types.Drop) (*models.DropPattern, bool, error) {
	originalFingerprint, hash := s.calculateDropPatternHash(drops)
	dropPattern := &models.DropPattern{
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

func (s *DropPatternRepo) calculateDropPatternHash(drops []*types.Drop) (originalFingerprint, hexHash string) {
	segments := make([]string, len(drops))

	for i, drop := range drops {
		segments[i] = fmt.Sprintf("%d:%d", drop.ItemID, drop.Quantity)
	}

	sort.Strings(segments)

	originalFingerprint = strings.Join(segments, "|")
	hash := xxh3.HashStringSeed(originalFingerprint, 0)
	return originalFingerprint, strconv.FormatUint(hash, 16)
}
