package reportverifs

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
)

var (
	ErrInvalidDropType      = errors.New("invalid drop type")
	ErrInvalidDropItem      = errors.New("invalid drop item")
	ErrInvalidDropInfoCount = errors.New("invalid drop info count")
	ErrUnknownItemID        = errors.New("unknown item id")
	ErrUnknownDropInfoTuple = errors.New("unknown drop type + item id tuple")
)

// verifyDrops returns how the drops of the report do not fit the drop infos of its stage, if they do not.
func (v *Verifier) verifyDrops(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) ([]error, error) {
	itemDropInfos, typeDropInfos, err := v.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repo.DropInfoQuery{
		Server:     reportTask.Server,
		ArkStageId: report.StageID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get drop infos")
	}

	adjustDropInfosByTimes(itemDropInfos, typeDropInfos, report.Times)

	if l := log.Trace(); l.Enabled() {
		l.Interface("itemDropInfos", itemDropInfos).
			Interface("typeDropInfos", typeDropInfos).
			Msg("verifying drop")
	}

	var errs []error

	if innerErrs := verifyDropType(report, typeDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	if innerErrs := verifyDropItem(report, itemDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	return errs, nil
}

func verifyDropType(report *types.ReportTaskSingleReport, dropInfos []*model.DropInfo) (errs []error) {
	grouped := lo.GroupBy(report.Drops, func(drop *types.Drop) string {
		return drop.DropType
	})

	// dropTypeAmountMap: key is drop type, value is amount (how many kinds of items are dropped under this type)
	dropTypeAmountMap := lo.MapValues(grouped, func(drops []*types.Drop, _ string) int {
		return len(drops)
	})

	if l := log.Trace(); l.Enabled() {
		l.Interface("grouped", grouped).
			Interface("dropTypeAmountMap", dropTypeAmountMap).
			Msg("dropTypeAmountMap")
	}

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

func adjustDropInfosByTimes(itemDropInfos []*model.DropInfo, typeDropInfos []*model.DropInfo, times int) {
	dropTypeItemTypeCountMap := make(map[string]int)

	for _, dropInfo := range itemDropInfos {
		dropTypeItemTypeCountMap[dropInfo.DropType] += 1
		// for item drop info, we need to adjust multiple the original bounds by times
		dropInfo.Bounds.Lower *= times
		dropInfo.Bounds.Upper *= times
		if dropInfo.Bounds.Exceptions != nil {
			// Adjusting exceptions by times will make the range very complicated, so we just remove it
			dropInfo.Bounds.Exceptions = nil
		}
	}

	for _, dropInfo := range typeDropInfos {
		// The lower bound remains the same, because no matter how many times you play, you can always get at least as many types as the lower bound
		// For upper bound, each time can give up to oldUpper types, so newUpper = max(oldUpper, min(max(oldUpper, 1) * times, item_type_count_for_this_drop_type))
		// max(oldUpper, 1) keeps one type per time for an oldUpper of 0, so the bound is never lower than the old max(oldUpper, min(times, item_type_count_for_this_drop_type))
		upper := dropInfo.Bounds.Upper
		if upper < 1 {
			upper = 1
		}
		upper *= times
		if dropTypeItemTypeCountMap[dropInfo.DropType] < upper {
			upper = dropTypeItemTypeCountMap[dropInfo.DropType]
		}
		if upper > dropInfo.Bounds.Upper {
			dropInfo.Bounds.Upper = upper
		}
		if dropInfo.Bounds.Exceptions != nil {
			dropInfo.Bounds.Exceptions = nil
		}
	}
}

type DropInfoTuple struct {
	ItemID   int64
	DropType string
}

/**
 * Verify drop item quantity
 * Check 1: iterate drops, check if any item is not in dropInfos
 * Check 2: iterate dropInfos, check if quantity is within bounds
 */
func verifyDropItem(report *types.ReportTaskSingleReport, dropInfos []*model.DropInfo) (errs []error) {
	dropInfoSetFromDropInfos := make(map[DropInfoTuple]struct{})
	for _, dropInfo := range dropInfos {
		tuple := DropInfoTuple{
			ItemID:   dropInfo.ItemID.Int64,
			DropType: dropInfo.DropType,
		}
		dropInfoSetFromDropInfos[tuple] = struct{}{}
	}

	// dropItemQuantityMap: key is item id, value is a sub map, key is drop type, value is quantity
	dropItemQuantityMap := make(map[int]map[string]int)
	for _, drop := range report.Drops {
		tuple := DropInfoTuple{
			ItemID:   int64(drop.ItemID),
			DropType: drop.DropType,
		}
		// Check 1
		if _, ok := dropInfoSetFromDropInfos[tuple]; !ok {
			errs = append(errs, errors.Wrap(ErrUnknownDropInfoTuple, fmt.Sprintf("dropInfo tuple (dropType %s, itemId %d) not found in drop info", drop.DropType, drop.ItemID)))
		}
		if _, ok := dropItemQuantityMap[drop.ItemID]; !ok {
			dropItemQuantityMap[drop.ItemID] = make(map[string]int)
		}
		dropItemQuantityMap[drop.ItemID][drop.DropType] += drop.Quantity
	}

	if l := log.Trace(); l.Enabled() {
		l.Interface("dropItemQuantityMap", dropItemQuantityMap).
			Msg("dropItemQuantityMap")
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
