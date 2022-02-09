package repos

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type DropPatternElementRepo struct {
	DB *bun.DB
}

func NewDropPatternElementRepo(db *bun.DB) *DropPatternElementRepo {
	return &DropPatternElementRepo{DB: db}
}

func (r *DropPatternElementRepo) GetDropPatternElementById(ctx context.Context, id int) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("id = ?", id).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElementRepo) GetDropPatternElementByHash(ctx context.Context, hash string) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElementRepo) CreateDropPatternElements(ctx context.Context, tx bun.Tx, patternId int, drops []*types.Drop) ([]*models.DropPatternElement, error) {
	var elements []*models.DropPatternElement
	for _, drop := range drops {
		element := &models.DropPatternElement{
			ItemID:        drop.ItemID,
			Quantity:      drop.Quantity,
			DropPatternID: patternId,
		}
		elements = append(elements, element)
	}

	_, err := r.DB.NewInsert().
		Model(&elements).
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	return elements, nil
}

func (r *DropPatternElementRepo) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*models.DropPatternElement, error) {
	var elements []*models.DropPatternElement
	err := cache.DropPatternElementsFromId.Get(strconv.Itoa(patternId), &elements)
	if err == nil {
		return elements, nil
	}

	err = r.DB.NewSelect().
		Model(&elements).
		Where("drop_pattern_id = ?", patternId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		elements = make([]*models.DropPatternElement, 0)
	} else if err != nil {
		return nil, err
	}

	go cache.DropPatternElementsFromId.Set(strconv.Itoa(patternId), &elements, time.Hour*24)
	return elements, nil
}
