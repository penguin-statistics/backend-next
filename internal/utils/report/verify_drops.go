package report

import (
	"context"
	"fmt"

	"github.com/ahmetb/go-linq/v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/models/konst"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropVerifier struct {
	DropInfoRepo *repos.DropInfoRepo
}

func NewDropVerifier(dropInfoRepo *repos.DropInfoRepo) *DropVerifier {
	return &DropVerifier{
		DropInfoRepo: dropInfoRepo,
	}
}

func (d *DropVerifier) Verify(ctx context.Context, report *dto.SingleReport) error {
	drops := report.Report.Drops
	tuples := make([][]string, 0, len(drops))
	var err error
	linq.From(drops).
		SelectT(func(drop dto.Drop) []string {
			mappedDropType, have := konst.DropTypeMap[drop.DropType]
			if !have {
				err = fmt.Errorf("invalid drop type: expected one of %v, but got `%s`", konst.DropTypeMapKeys, drop.DropType)
				return []string{}
			}
			return []string{
				drop.ItemID,
				mappedDropType,
			}
		}).
		ToSlice(&tuples)
	if err != nil {
		return err
	}

	itemDropInfos, typeDropInfos, err := d.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repos.DropInfoQuery{
		Server:     report.Report.Server,
		ArkStageId: report.Report.StageID,
		DropTuples: tuples,
	})
	if err != nil {
		return err
	}

	if len(itemDropInfos) != len(drops) {
		return fmt.Errorf("invalid drop info count: expected %d, but got %d", len(drops), len(itemDropInfos))
	}

	if err = d.verifyDropType(ctx, report, typeDropInfos); err != nil {
		return err
	}

	if err = d.verifyDropItem(ctx, report, itemDropInfos); err != nil {
		return err
	}

	return nil
}

func (d *DropVerifier) verifyDropType(ctx context.Context, report *dto.SingleReport, dropInfos []*models.DropInfo) error {
	dropTypeAmountMap := make(map[string]int)
	linq.From(report.Report.Drops).
		SelectT(func(drop dto.Drop) string {
			// only pick dropType
			return drop.DropType
		}).
		GroupByT(func(dropType string) string {
			return dropType
		}, func(dropType string) string {
			return dropType
		}).
		ToMapByT(&dropTypeAmountMap, func(dropTypeGroup linq.Group) string {
			return dropTypeGroup.Key.(string)
		}, func(dropTypeGroup linq.Group) int {
			return len(dropTypeGroup.Group)
		})

	for _, dropInfo := range dropInfos {
		for dropType, count := range dropTypeAmountMap {
			if dropInfo.Bounds.Lower > count {
				return fmt.Errorf("drop type `%s`: expected at least %d, but got %d", dropType, dropInfo.Bounds.Lower, count)
			} else if dropInfo.Bounds.Upper < count {
				return fmt.Errorf("drop type `%s`: expected at most %d, but got %d", dropType, dropInfo.Bounds.Upper, count)
			}
			if dropInfo.Bounds.Exceptions != nil {
				if linq.From(dropInfo.Bounds.Exceptions).AnyWithT(func(exception int) bool {
					return exception == count
				}) {
					return fmt.Errorf("drop type `%s`: expected not to have %d", dropType, count)
				}
			}
		}
	}

	return nil
}

func (d *DropVerifier) verifyDropItem(ctx context.Context, report *dto.SingleReport, dropInfos []*models.DropInfo) error {
	for _, dropInfo := range dropInfos {
		for _, drop := range report.Report.Drops {
			count := drop.Quantity
			if dropInfo.Bounds.Lower > count {
				return fmt.Errorf("drop item `%s`: expected at least %d, but got %d", drop.ItemID, dropInfo.Bounds.Lower, count)
			} else if dropInfo.Bounds.Upper < count {
				return fmt.Errorf("drop item `%s`: expected at most %d, but got %d", drop.ItemID, dropInfo.Bounds.Upper, count)
			}
			if dropInfo.Bounds.Exceptions != nil {
				if linq.From(dropInfo.Bounds.Exceptions).AnyWithT(func(exception int) bool {
					return exception == count
				}) {
					return fmt.Errorf("drop item `%s`: expected not to have %d", drop.ItemID, count)
				}
			}
		}
	}

	return nil
}
