package reportverifs

import (
	"context"
	"fmt"
	"time"

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

// stageDropInfos are the drop infos of a stage and server, split as GetForCurrentTimeRangeWithDropTypes splits them.
// Those of Verifier.dropInfos are read-only: verifyDrops adjusts copies of them.
type stageDropInfos struct {
	items, types []*model.DropInfo
}

// currentDropInfos returns copies of the drop infos of query's stage and server in their current time range, as read up
// to cacheTTL ago, and never after the end of one of their time ranges: a time range ending is seen when it ends, one
// starting up to cacheTTL late. No drop infos are cached as any others; an error is returned as read, and not cached.
func (v *Verifier) currentDropInfos(ctx context.Context, query repo.DropInfoQuery) (itemDropInfos, typeDropInfos []*model.DropInfo, err error) {
	now := v.clock()
	cached, err := v.dropInfos.get(ctx, now, query, func() (stageDropInfos, time.Time, error) {
		items, types, until, err := v.DropInfoRepo.GetForCurrentTimeRangeWithDropTypesUntil(ctx, &query)
		return stageDropInfos{items: items, types: types}, dropInfosExpiry(now, until), err
	})
	if err != nil {
		return nil, nil, err
	}
	itemDropInfos, typeDropInfos = copyDropInfos(cached.items, cached.types)
	return itemDropInfos, typeDropInfos, nil
}

// dropInfosExpiry returns when drop infos read at now expire: cacheTTL later, or at until, the end of the first of their
// time ranges to end, if it is earlier (until is zero if none ends). Drop infos whose time range ended by now (by the
// clock of the database, which may be a little behind) are expired already.
func dropInfosExpiry(now, until time.Time) time.Time {
	expiry := now.Add(cacheTTL)
	if !until.IsZero() && until.Before(expiry) {
		return until
	}
	return expiry
}

// copyDropInfos returns copies of items and types, bounds included, in three allocations. An empty one is nil, as read.
func copyDropInfos(items, types []*model.DropInfo) (itemCopies, typeCopies []*model.DropInfo) {
	n := len(items) + len(types)
	if n == 0 {
		return nil, nil
	}
	values := make([]model.DropInfo, n)
	bounds := make([]model.Bounds, n)
	copies := make([]*model.DropInfo, n)
	copyTo := func(i int, dropInfo *model.DropInfo) {
		values[i] = *dropInfo
		if dropInfo.Bounds != nil {
			bounds[i] = *dropInfo.Bounds
			if dropInfo.Bounds.Exceptions != nil {
				bounds[i].Exceptions = append([]int(nil), dropInfo.Bounds.Exceptions...)
			}
			values[i].Bounds = &bounds[i]
		}
		copies[i] = &values[i]
	}
	for i, dropInfo := range items {
		copyTo(i, dropInfo)
	}
	for i, dropInfo := range types {
		copyTo(len(items)+i, dropInfo)
	}
	if len(items) > 0 {
		itemCopies = copies[:len(items):len(items)]
	}
	if len(types) > 0 {
		typeCopies = copies[len(items):]
	}
	return itemCopies, typeCopies
}

// verifyDrops returns how the drops of the report do not fit the drop infos of its stage, if they do not.
func (v *Verifier) verifyDrops(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) ([]error, error) {
	itemDropInfos, typeDropInfos, err := v.currentDropInfos(ctx, repo.DropInfoQuery{
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
	// dropTypeGuaranteedCountMap: key is drop type, value is how many types of items of this drop type drop every time (item lower bound above 0)
	dropTypeGuaranteedCountMap := make(map[string]int)

	for _, dropInfo := range itemDropInfos {
		dropTypeItemTypeCountMap[dropInfo.DropType] += 1
		if dropInfo.Bounds.Lower > 0 {
			dropTypeGuaranteedCountMap[dropInfo.DropType] += 1
		}
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
		// For upper bound, each time gives the guaranteed types (item lower bound above 0) and up to oldUpper - guaranteed other types,
		// so times times can give up to guaranteed + times * (oldUpper - guaranteed) types, and at most item_type_count_for_this_drop_type.
		// The upper bound was max(oldUpper, min(times, item_type_count_for_this_drop_type)) before, and it is kept as a floor, so no report accepted before is rejected
		// (for example for an oldUpper of 0, or an oldUpper below guaranteed), and the new term only counts for times above 1, so times 0 and 1 keep the old bound:
		// newUpper = max(oldUpper, min(times, item_type_count_for_this_drop_type), min(guaranteed + times * (oldUpper - guaranteed), item_type_count_for_this_drop_type))
		itemTypeCount := dropTypeItemTypeCountMap[dropInfo.DropType]
		guaranteed := dropTypeGuaranteedCountMap[dropInfo.DropType]
		upper := times
		if times > 1 {
			if possible := guaranteed + times*(dropInfo.Bounds.Upper-guaranteed); possible > upper {
				upper = possible
			}
		}
		if itemTypeCount < upper {
			upper = itemTypeCount
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
