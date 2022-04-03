package reportutil

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

var (
	ErrInvalidDropType      = errors.New("invalid drop type")
	ErrInvalidDropItem      = errors.New("invalid drop item")
	ErrInvalidDropInfoCount = errors.New("invalid drop info count")
	ErrUnknownItemID        = errors.New("unknown item id")
)

type DropVerifier struct {
	DropInfoRepo *repo.DropInfo
}

func NewDropVerifier(dropInfoRepo *repo.DropInfo) *DropVerifier {
	return &DropVerifier{
		DropInfoRepo: dropInfoRepo,
	}
}

func (d *DropVerifier) Name() string {
	return "drop"
}

func (d *DropVerifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) (errs []error) {
	itemDropInfos, typeDropInfos, err := d.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repo.DropInfoQuery{
		Server:     reportTask.Server,
		ArkStageId: report.StageID,
	})
	if err != nil {
		errs = append(errs, err)
	}

	if innerErrs := d.verifyDropType(report, typeDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	if innerErrs := d.verifyDropItem(report, itemDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	return errs
}

func (d *DropVerifier) verifyDropType(report *types.SingleReport, dropInfos []*models.DropInfo) (errs []error) {
	grouped := lo.GroupBy(report.Drops, func(drop *types.Drop) string {
		return drop.DropType
	})

	// dropTypeAmountMap: key is drop type, value is amount (how many kinds of items are dropped under this type)
	dropTypeAmountMap := lo.MapValues(grouped, func(drops []*types.Drop, _ string) int {
		return len(drops)
	})

	for _, dropInfo := range dropInfos {
		count := dropTypeAmountMap[dropInfo.DropType]
		if dropInfo.Bounds.Lower > count {
			errs = append(errs, errors.Wrap(ErrInvalidDropType, fmt.Sprintf("drop type `%s`: expected at least %d, but got %d", dropInfo.DropType, dropInfo.Bounds.Lower, count)))
		} else if dropInfo.Bounds.Upper < count {
			errs = append(errs, errors.Wrap(ErrInvalidDropType, fmt.Sprintf("drop type `%s`: expected at most %d, but got %d", dropInfo.DropType, dropInfo.Bounds.Upper, count)))
		} else if dropInfo.Bounds.Exceptions != nil {
			if lo.Contains(dropInfo.Bounds.Exceptions, count) {
				errs = append(errs, errors.Wrap(ErrInvalidDropType, fmt.Sprintf("drop type `%s`: expected not to have (%v), but got %d", dropInfo.DropType, dropInfo.Bounds.Exceptions, count)))
			}
		}
	}

	return errs
}

/**
 * Verify drop item quantity
 * Check 1: iterate drops, check if any item is not in dropInfos
 * Check 2: iterate dropInfos, check if quantity is within bounds
 */
func (d *DropVerifier) verifyDropItem(report *types.SingleReport, dropInfos []*models.DropInfo) (errs []error) {
	itemIdSetFromDropInfos := make(map[int]struct{})
	for _, dropInfo := range dropInfos {
		itemIdSetFromDropInfos[int(dropInfo.ItemID.Int64)] = struct{}{}
	}

	// dropItemQuantityMap: key is item id, value is a sub map, key is drop type, value is quantity
	dropItemQuantityMap := make(map[int]map[string]int)
	for _, drop := range report.Drops {
		// Check 1
		if _, ok := itemIdSetFromDropInfos[drop.ItemID]; !ok {
			errs = append(errs, errors.Wrap(ErrUnknownItemID, fmt.Sprintf("item ID %d not found in drop info", drop.ItemID)))
		}
		if _, ok := dropItemQuantityMap[drop.ItemID]; !ok {
			dropItemQuantityMap[drop.ItemID] = make(map[string]int)
		}
		dropItemQuantityMap[drop.ItemID][drop.DropType] += drop.Quantity
	}

	// Check 2
	for _, dropInfo := range dropInfos {
		itemId := int(dropInfo.ItemID.Int64)
		count := 0
		if quantityMap, ok := dropItemQuantityMap[itemId]; ok {
			count = quantityMap[dropInfo.DropType]
		}
		if dropInfo.Bounds.Lower > count {
			errs = append(errs, errors.Wrap(ErrInvalidDropItem, fmt.Sprintf("item %d in drop type `%s`: expected at least %d, but got %d", itemId, dropInfo.DropType, dropInfo.Bounds.Lower, count)))
		} else if dropInfo.Bounds.Upper < count {
			errs = append(errs, errors.Wrap(ErrInvalidDropItem, fmt.Sprintf("item %d in drop type `%s`: expected at most %d, but got %d", itemId, dropInfo.DropType, dropInfo.Bounds.Upper, count)))
		} else if dropInfo.Bounds.Exceptions != nil {
			if lo.Contains(dropInfo.Bounds.Exceptions, count) {
				errs = append(errs, errors.Wrap(ErrInvalidDropItem, fmt.Sprintf("item %d in drop type `%s`: expected not to have (%v), but got %d", itemId, dropInfo.DropType, dropInfo.Bounds.Exceptions, count)))
			}
		}
	}

	return errs
}
