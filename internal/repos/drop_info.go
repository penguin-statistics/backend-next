package repos

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgqry"
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

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropInfo, nil
}

func (s *DropInfoRepo) GetDropInfosByServerAndStageId(ctx context.Context, server string, stageId int) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := s.DB.NewSelect().
		Model(&dropInfo).
		Where("stage_id = ?", stageId).
		Where("server = ?", server).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return dropInfo, nil
}

func (s *DropInfoRepo) GetDropInfosByServer(ctx context.Context, server string) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := s.DB.NewSelect().
		Model(&dropInfo).
		Where("server = ?", server).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return dropInfo, nil
}

type DropInfoQuery struct {
	Server     string
	ArkStageId string
}

// GetDropInfoByArkId returns a drop info by its ark id.
func (s *DropInfoRepo) GetForCurrentTimeRange(ctx context.Context, query *DropInfoQuery) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := pgqry.New(
		s.DB.NewSelect().
			Model(&dropInfo).
			Where("di.server = ?", query.Server).
			Where("st.ark_stage_id = ?", query.ArkStageId),
	).
		UseItemById("di.item_id").
		UseStageById("di.stage_id").
		UseTimeRange("di.range_id").
		DoFilterCurrentTimeRange().
		Q.Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return dropInfo, nil
}

func (s *DropInfoRepo) GetItemDropSetByStageIdAndRangeId(ctx context.Context, server string, stageId int, rangeId int) ([]int, error) {
	var results []any
	err := pgqry.New(
		s.DB.NewSelect().
			Column("di.item_id").
			Model((*models.DropInfo)(nil)).
			Where("di.server = ?", server).
			Where("di.stage_id = ?", stageId).
			Where("di.item_id IS NOT NULL").
			Where("di.range_id = ?", rangeId),
	).Q.Scan(ctx, &results)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	linq.From(results).
		SelectT(func(el any) int { return int(el.(int64)) }).
		Distinct().
		SortT(func(a int, b int) bool { return a < b }).
		ToSlice(&results)

	itemIds := make([]int, len(results))
	for i := range results {
		itemIds[i] = results[i].(int)
	}
	return itemIds, nil
}

func (s *DropInfoRepo) GetForCurrentTimeRangeWithDropTypes(ctx context.Context, query *DropInfoQuery) (itemDropInfos, typeDropInfos []*models.DropInfo, err error) {
	allDropInfos, err := s.GetForCurrentTimeRange(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	for _, dropInfo := range allDropInfos {
		if dropInfo.DropType != constants.DropTypeRecognitionOnly {
			if dropInfo.ItemID.Valid {
				itemDropInfos = append(itemDropInfos, dropInfo)
			} else {
				typeDropInfos = append(typeDropInfos, dropInfo)
			}
		}
	}

	return itemDropInfos, typeDropInfos, nil
}

func (s *DropInfoRepo) GetDropInfosWithFilters(ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	results := make([]*models.DropInfo, 0)
	var whereBuilder strings.Builder
	fmt.Fprintf(&whereBuilder, "di.server = ? AND di.drop_type != ? AND di.item_id IS NOT NULL")

	if len(stageIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.stage_id IN (?)")
	}
	if len(itemIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.item_id IN (?)")
	}

	allTimeRangesHaveNoRangeId := true
	for _, timeRange := range timeRanges {
		if timeRange.RangeID > 0 {
			allTimeRangesHaveNoRangeId = false
			break
		}
	}

	if len(timeRanges) > 0 {
		if allTimeRangesHaveNoRangeId {
			for _, timeRange := range timeRanges {
				startTimeStr := timeRange.StartTime.Format(time.RFC3339)
				endTimeStr := timeRange.EndTime.Format(time.RFC3339)
				fmt.Fprintf(&whereBuilder,
					" AND tr.start_time <= timestamp with time zone '%s' AND tr.end_time >= timestamp with time zone '%s'",
					endTimeStr,
					startTimeStr)
			}
		} else {
			if len(timeRanges) == 1 {
				fmt.Fprintf(&whereBuilder, " AND di.range_id = %d", timeRanges[0].RangeID)
			} else {
				rangeIdStr := make([]string, len(timeRanges))
				linq.From(timeRanges).SelectT(func(timeRange *models.TimeRange) string { return strconv.Itoa(timeRange.RangeID) }).ToSlice(&rangeIdStr)
				fmt.Fprintf(&whereBuilder, " AND di.range_id IN (%s)", strings.Join(rangeIdStr, ","))
			}
		}
	}
	if err := s.DB.NewSelect().TableExpr("drop_infos as di").Column("di.stage_id", "di.item_id", "di.accumulable").
		Where(whereBuilder.String(), server, constants.DropTypeRecognitionOnly, bun.In(stageIdFilter), bun.In(itemIdFilter)).
		Join("JOIN time_ranges AS tr ON tr.range_id = di.range_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}
