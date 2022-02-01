package repos

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
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

func (s *DropInfoRepo) GetDropInfosByServerAndStageId(ctx context.Context, server string, stageId int) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := s.DB.NewSelect().
		Model(&dropInfo).
		Where("stage_id = ?", stageId).
		Where("server = ?", server).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
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

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return dropInfo, nil
}

type DropInfoQuery struct {
	Server     string
	ArkStageId string
	// DropTuples is in form of [](drop_item_id, drop_item_type)
	DropTuples [][]string

	withDropTypes *[]string
}

// GetDropInfoByArkId returns a drop info by its ark id.
// dropInfoTuples: [](drop_item_id, drop_item_type)
func (s *DropInfoRepo) GetForCurrentTimeRange(ctx context.Context, query *DropInfoQuery) ([]*models.DropInfo, error) {
	var dropInfo []*models.DropInfo
	err := pquery.New(
		s.DB.NewSelect().
			Model(&dropInfo).
			Where("di.server = ?", query.Server).
			Where("st.ark_stage_id = ?", query.ArkStageId).
			WhereGroup(" AND ", func(sq *bun.SelectQuery) *bun.SelectQuery {
				return sq.
					Where("(it.ark_item_id, di.drop_type) IN (?)", bun.In(query.DropTuples)).
					WhereGroup(" OR ", func(sq *bun.SelectQuery) *bun.SelectQuery {
						if query.withDropTypes == nil {
							return sq
						}
						return sq.
							Where("di.item_id IS NULL").
							Where("di.drop_type IN (?)", bun.In(*query.withDropTypes))
					})
			}),
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

func (s *DropInfoRepo) GetItemDropSetByStageIdAndRangeId(ctx context.Context, server string, stageId int, rangeId int) ([]int, error) {
	var results []interface{}
	err := pquery.New(
		s.DB.NewSelect().
			Column("di.item_id").
			Model((*models.DropInfo)(nil)).
			Where("di.server = ?", server).
			Where("di.stage_id = ?", stageId).
			Where("di.item_id IS NOT NULL").
			Where("di.range_id = ?", rangeId),
	).Q.Scan(ctx, &results)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	linq.From(results).
		SelectT(func(el interface{}) int { return int(el.(int64)) }).
		Distinct().
		SortT(func(a int, b int) bool { return a < b }).
		ToSlice(&results)

	itemIds := make([]int, len(results))
	for i := range results {
		itemIds[i] = results[i].(int)
	}
	return itemIds, nil
}

func (s *DropInfoRepo) GetForCurrentTimeRangeWithDropTypes(ctx context.Context, query *DropInfoQuery) ([]*models.DropInfo, []*models.DropInfo, error) {
	var typesToInclude []string

	// get distinct drop types
	linq.From(query.DropTuples).
		SelectT(func(tuple []string) string {
			return tuple[1] // select drop types
		}).
		Distinct().
		SelectT(func(dropType string) string {
			return dropType
		}).
		ToSlice(&typesToInclude)

	query.withDropTypes = &typesToInclude
	allDropInfos, err := s.GetForCurrentTimeRange(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	var itemDropInfos []*models.DropInfo
	var typeDropInfos []*models.DropInfo
	for _, dropInfo := range allDropInfos {
		if dropInfo.ItemID.Valid {
			itemDropInfos = append(itemDropInfos, dropInfo)
		} else {
			typeDropInfos = append(typeDropInfos, dropInfo)
		}
	}

	return itemDropInfos, typeDropInfos, nil
}

func (s *DropInfoRepo) GetDropInfosWithFilters(ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	results := make([]*models.DropInfo, 0)
	var whereBuilder strings.Builder
	fmt.Fprintf(&whereBuilder, "di.server = ? AND di.drop_type != ? AND di.item_id IS NOT NULL")

	if stageIdFilter != nil && len(stageIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.stage_id IN (?)")
	}
	if itemIdFilter != nil && len(itemIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.item_id IN (?)")
	}

	allTimeRangesHaveNoRangeId := true
	for _, timeRange := range timeRanges {
		if timeRange.RangeID > 0 {
			allTimeRangesHaveNoRangeId = false
			break	
		}
	}

	if timeRanges != nil && len(timeRanges) > 0 {
		if allTimeRangesHaveNoRangeId {
			for _, timeRange := range timeRanges {
				fmt.Fprintf(&whereBuilder,
					" AND tr.start_time >= timestamp with time zone '%s' AND tr.end_time <= timestamp with time zone '%s'",
					timeRange.StartTime.Format(time.RFC3339),
					timeRange.EndTime.Format(time.RFC3339))
			}
		} else {
			if len(timeRanges) == 1 {
				fmt.Fprintf(&whereBuilder, " AND di.range_id = %d", timeRanges[0].RangeID)
			} else {
				rangeIdStr := make([]string, len(timeRanges))
				linq.From(timeRanges).SelectT(func (timeRange *models.TimeRange) string { return strconv.FormatInt(int64(timeRange.RangeID), 10) }).ToSlice(&rangeIdStr)
				fmt.Fprintf(&whereBuilder, " AND di.range_id IN (%s)", strings.Join(rangeIdStr, ","))
			}
		}
	}
	if err := s.DB.NewSelect().TableExpr("drop_infos as di").Column("di.stage_id", "di.item_id", "di.accumulable").
		Where(whereBuilder.String(), server, "RECOGNITION_ONLY", bun.In(stageIdFilter), bun.In(itemIdFilter)).
		Join("JOIN time_ranges AS tr ON tr.range_id = di.range_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}
