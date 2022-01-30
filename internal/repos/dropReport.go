package repos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
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

func (s *DropReportRepo) CalcTotalQuantity(ctx context.Context, server string, timeRange *models.TimeRange, dropInfos []*models.DropInfo, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	dropInfosByStageId := groupDropInfosByStageId(dropInfos)
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND (")
	for idx, el := range dropInfosByStageId {
		stageId := el.Key.(int)
		var itemIds []int
		linq.From(el.Group).
			SelectT(func(dropInfo *models.DropInfo) int {
				return int(dropInfo.ItemID.Int64)
			}).
			ToSlice(&itemIds)

		fmt.Fprintf(&b, "dr.stage_id = %d AND dpe.item_id", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&b, " = %d", itemIds[0])
		} else {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.FormatInt(int64(itemId), 10))
			}
			fmt.Fprintf(&b, " IN (%s)", strings.Join(itemIdsStr, ","))
		}
		if idx != len(dropInfosByStageId)-1 {
			b.WriteString(" OR ")
		}
	}
	b.WriteString(")")

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if accountId.Valid {
		query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query.Where("dr.reliable = true")
	}
	if err := query.
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimes(ctx context.Context, server string, timeRange *models.TimeRange, dropInfos []*models.DropInfo, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	dropInfosByStageId := groupDropInfosByStageId(dropInfos)
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND dr.stage_id")
	var stageIds []int
	linq.From(dropInfosByStageId).
		SelectT(func(group linq.Group) int { return group.Key.(int) }).
		Distinct().
		SortT(func(a int, b int) bool { return a < b }).
		ToSlice(&stageIds)
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
		ColumnExpr("COUNT(*) AS total_times").
		Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if accountId.Valid {
		query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query.Where("dr.reliable = true")
	}
	if err := query.
		Group("dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func groupDropInfosByStageId(dropInfos []*models.DropInfo) []linq.Group {
	var dropInfosByStageId []linq.Group
	linq.From(dropInfos).
		GroupByT(
			func(dropInfo *models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *models.DropInfo) *models.DropInfo { return dropInfo }).
		ToSlice(&dropInfosByStageId)
	return dropInfosByStageId
}
