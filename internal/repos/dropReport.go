package repos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type DropReportRepo struct {
	DB *bun.DB
}

func NewDropReportRepo(db *bun.DB) *DropReportRepo {
	return &DropReportRepo{DB: db}
}

func (s *DropReportRepo) CreateDropReport(ctx context.Context, tx bun.Tx, dropReport *models.DropReport) error {
	_, err := tx.NewInsert().
		Model(dropReport).
		ExcludeColumn("created_at").
		Exec(ctx)
	return err
}

func (s *DropReportRepo) CalcTotalQuantityForDropMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	stageConditions := make([]string, 0)
	for stageId, itemIds := range stageIdItemIdMap {
		var stageB strings.Builder
		fmt.Fprintf(&stageB, "dr.stage_id = %d AND dpe.item_id", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&stageB, " = %d", itemIds[0])
		} else {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.FormatInt(int64(itemId), 10))
			}
			fmt.Fprintf(&stageB, " IN (%s)", strings.Join(itemIdsStr, ","))
		}
		stageConditions = append(stageConditions, stageB.String())
	}
	fmt.Fprintf(&b, " AND (%s)", strings.Join(stageConditions, " OR "))

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalQuantityForPatternMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIds) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND dr.times = ? AND "+b.String(), server, 1)
	if err := query.
		Group("dr.stage_id", "dr.pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimes(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int, excludeNonOneTimes bool) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIds) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id").
		ColumnExpr("COUNT(*) AS total_times")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	if excludeNonOneTimes {
		query = query.Where("dr.times = 1")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}
