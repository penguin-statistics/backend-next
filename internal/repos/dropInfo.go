package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/utils/pquery"
)

type DropInfoRepo struct {
	DB *bun.DB
}

func NewDropInfoRepo(db *bun.DB) *DropInfoRepo {
	return &DropInfoRepo{DB: db}
}

func (s *DropInfoRepo) GetDropInfo(ctx context.Context, id int) (*models.DropInfo, error) {
	var dropInfo models.DropInfo
	err := s.DB.NewSelect().
		Model(&dropInfo).
		Where("id = ?", id).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropInfo, nil
}

func (s *DropInfoRepo) GetCurrentTimeRangeDropInfo(ctx context.Context, server, arkStageId string, tuples [][]string) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := pquery.New(
		s.DB.NewSelect().
			Model(&dropInfo).
			Where("di.server = ?", server).
			Where("(it.ark_item_id, di.drop_type) IN (?)", bun.In(tuples)).
			Where("st.ark_stage_id = ?", arkStageId),
	).
		UseItemById("di.item_id").
		UseStageById("di.stage_id").
		UseTimeRange("di.range_id").
		DoFilterCurrentTimeRange().
		Q.Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return dropInfo, nil
}
