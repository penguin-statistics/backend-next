package reportverifs

import (
	"context"
	"fmt"
	"strings"

	"exusiai.dev/gommon/constant"
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

type DropVerifier struct {
	DropInfoRepo *repo.DropInfo
}

// ensure DropVerifier conforms to Verifier
var _ Verifier = (*DropVerifier)(nil)

func NewDropVerifier(dropInfoRepo *repo.DropInfo) *DropVerifier {
	return &DropVerifier{
		DropInfoRepo: dropInfoRepo,
	}
}

func (d *DropVerifier) Name() string {
	return "drop"
}

func (d *DropVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection {
	itemDropInfos, typeDropInfos, err := d.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx, &repo.DropInfoQuery{
		Server:     reportTask.Server,
		ArkStageId: report.StageID,
	})
	if err != nil {
		return &Rejection{
			Reliability: constant.ViolationReliabilityDrop,
			Message:     err.Error(),
		}
	}

	d.adjustDropInfosByTimes(itemDropInfos, typeDropInfos, report.Times)

	if l := log.Trace(); l.Enabled() {
		l.Interface("itemDropInfos", itemDropInfos).
			Interface("typeDropInfos", typeDropInfos).
			Msg("verifying drop")
	}

	var errs []error

	if innerErrs := d.verifyDropType(report, typeDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	if innerErrs := d.verifyDropItem(report, itemDropInfos); innerErrs != nil {
		errs = append(errs, innerErrs...)
	}

	if len(errs) > 0 {
		var b strings.Builder
		for i, err := range errs {
			b.WriteString(err.Error())
			if i < len(errs)-1 {
				b.WriteString(", ")
			}
		}

		return &Rejection{
			Reliability: constant.ViolationReliabilityDrop,
			Message:     b.String(),
		}
	}

	return nil
}

func (d *DropVerifier) verifyDropType(report *types.ReportTaskSingleReport, dropInfos []*model.DropInfo) (errs []error) {
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

func (d *DropVerifier) adjustDropInfosByTimes(itemDropInfos []*model.DropInfo, typeDropInfos []*model.DropInfo, times int) {
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
		// For upper bound, if times < item_type_count_for_this_drop_type, it means it's impossible to get each type at least once, so we adjust upper bound to be max(oldUpper, times)
		// if times > item_type_count_for_this_drop_type, it means you are able to get each type at lease once, so we adjust upper bound to be max(oldUpper, item_type_count_for_this_drop_type)
		// newUpper = max(oldUpper, min(times, item_type_count_for_this_drop_type))
		minBetweenTimesAndItemTypeCount := times
		if dropTypeItemTypeCountMap[dropInfo.DropType] < minBetweenTimesAndItemTypeCount {
			minBetweenTimesAndItemTypeCount = dropTypeItemTypeCountMap[dropInfo.DropType]
		}
		if minBetweenTimesAndItemTypeCount > dropInfo.Bounds.Upper {
			dropInfo.Bounds.Upper = minBetweenTimesAndItemTypeCount
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
func (d *DropVerifier) verifyDropItem(report *types.ReportTaskSingleReport, dropInfos []*model.DropInfo) (errs []error) {
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
