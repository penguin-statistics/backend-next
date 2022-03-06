package reportutils

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ahmetb/go-linq/v3"
	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

var ErrInvalidDropType = errors.New("invalid drop type")

type DropVerifier struct {
	DropInfoRepo *repos.DropInfoRepo
}

func NewDropVerifier(dropInfoRepo *repos.DropInfoRepo) *DropVerifier {
	return &DropVerifier{
		DropInfoRepo: dropInfoRepo,
	}
}

func (d *DropVerifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) error {
	drops := report.Drops
	tuples := make([][]string, 0, len(drops))
	var err error
	linq.From(drops).
		SelectT(func(drop *types.Drop) []string {
			return []string{
				strconv.Itoa(drop.ItemID),
				drop.DropType,
			}
		}).
		ToSlice(&tuples)
	if err != nil {
		return err
	}

	itemDropInfos, typeDropInfos, err := d.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repos.DropInfoQuery{
		Server:     reportTask.Server,
		ArkStageId: report.StageID,
		DropTuples: tuples,
	})
	if err != nil {
		return err
	}

	if len(itemDropInfos) != len(drops) {
		return fmt.Errorf("invalid drop info count: expected %d, but got %d", len(drops), len(itemDropInfos))
	}

	if err = d.verifyDropType(report, typeDropInfos); err != nil {
		return err
	}

	if err = d.verifyDropItem(report, itemDropInfos); err != nil {
		return err
	}

	return nil
}

func (d *DropVerifier) verifyDropType(report *types.SingleReport, dropInfos []*models.DropInfo) error {
	dropTypeAmountMap := make(map[string]int)
	linq.From(report.Drops).
		SelectT(func(drop *types.Drop) string {
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
		count := dropTypeAmountMap[dropInfo.DropType]
		if dropInfo.Bounds.Lower > count {
			return fmt.Errorf("drop type `%s`: expected at least %d, but got %d", dropInfo.DropType, dropInfo.Bounds.Lower, count)
		} else if dropInfo.Bounds.Upper < count {
			return fmt.Errorf("drop type `%s`: expected at most %d, but got %d", dropInfo.DropType, dropInfo.Bounds.Upper, count)
		}
		if dropInfo.Bounds.Exceptions != nil {
			if linq.From(dropInfo.Bounds.Exceptions).AnyWithT(func(exception int) bool {
				return exception == count
			}) {
				return fmt.Errorf("drop type `%s`: expected not to have %d", dropInfo.DropType, count)
			}
		}
	}

	return nil
}

func (d *DropVerifier) verifyDropItem(report *types.SingleReport, dropInfos []*models.DropInfo) error {
	for _, dropInfo := range dropInfos {
		for _, drop := range report.Drops {
			if drop.DropType != dropInfo.DropType || drop.ItemID != int(dropInfo.ItemID.Int64) {
				continue
			}
			count := drop.Quantity
			if dropInfo.Bounds.Lower > count {
				return fmt.Errorf("drop item `%d`: expected at least %d, but got %d", drop.ItemID, dropInfo.Bounds.Lower, count)
			} else if dropInfo.Bounds.Upper < count {
				return fmt.Errorf("drop item `%d`: expected at most %d, but got %d", drop.ItemID, dropInfo.Bounds.Upper, count)
			}
			if dropInfo.Bounds.Exceptions != nil {
				if linq.From(dropInfo.Bounds.Exceptions).AnyWithT(func(exception int) bool {
					return exception == count
				}) {
					return fmt.Errorf("drop item `%d`: expected not to have %d", drop.ItemID, count)
				}
			}
		}
	}

	return nil
}
