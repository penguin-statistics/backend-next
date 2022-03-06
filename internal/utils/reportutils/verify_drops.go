package reportutils

import (
	"context"
	"fmt"

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
	itemDropInfos, typeDropInfos, err := d.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repos.DropInfoQuery{
		Server:     reportTask.Server,
		ArkStageId: report.StageID,
	})
	if err != nil {
		return err
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
	// dropTypeAmountMap: key is drop type, value is amount (how many kinds of items are dropped under this type)
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

/**
 * Verify drop item quantity
 * Check 1: iterate drops, check if any item is not in dropInfos
 * Check 2: iterate dropInfos, check if quantity is within bounds
 */
func (d *DropVerifier) verifyDropItem(report *types.SingleReport, dropInfos []*models.DropInfo) error {
	itemIdSetFromDropInfos := make(map[int]struct{})
	for _, dropInfo := range dropInfos {
		itemIdSetFromDropInfos[int(dropInfo.ItemID.Int64)] = struct{}{}
	}

	// dropItemQuantityMap: key is item id, value is a sub map, key is drop type, value is quantity
	dropItemQuantityMap := make(map[int]map[string]int)
	for _, drop := range report.Drops {
		// Check 1
		if _, ok := itemIdSetFromDropInfos[int(drop.ItemID)]; !ok {
			return fmt.Errorf("item id %d: not found in drop info", drop.ItemID)
		}
		if _, ok := dropItemQuantityMap[drop.ItemID]; !ok {
			dropItemQuantityMap[drop.ItemID] = make(map[string]int)
		}
		dropItemQuantityMap[drop.ItemID][drop.DropType] += drop.Quantity
	}

	// Check 2
	for _, dropInfo := range dropInfos {
		itemId := int(dropInfo.ItemID.Int64)
		quantity := 0
		if quantityMap, ok := dropItemQuantityMap[itemId]; ok {
			quantity = quantityMap[dropInfo.DropType]
		}
		if dropInfo.Bounds.Lower > quantity {
			return fmt.Errorf("drop item `%d`: expected at least %d, but got %d", itemId, dropInfo.Bounds.Lower, quantity)
		} else if dropInfo.Bounds.Upper < quantity {
			return fmt.Errorf("drop item `%d`: expected at most %d, but got %d", itemId, dropInfo.Bounds.Upper, quantity)
		}
		if dropInfo.Bounds.Exceptions != nil {
			if linq.From(dropInfo.Bounds.Exceptions).AnyWithT(func(exception int) bool {
				return exception == quantity
			}) {
				return fmt.Errorf("drop item `%d`: expected not to have %d", itemId, quantity)
			}
		}
	}

	return nil
}
