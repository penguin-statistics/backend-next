package service

import (
	"context"

	"github.com/samber/lo"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

type DropMatrixElement struct {
	DropMatrixElementRepo *repo.DropMatrixElement
}

func NewDropMatrixElement(dropMatrixElementRepo *repo.DropMatrixElement) *DropMatrixElement {
	return &DropMatrixElement{
		DropMatrixElementRepo: dropMatrixElementRepo,
	}
}

// elementsOfDay returns those of elements that are of dayNum, for a replacement of the day. The calculation of a day
// covers later days too: with an open end (the job's today), the time ranges starting after the day; with the start
// of the next day as end, a time range starting then, over an empty window. Neither window holds reports yet, so
// they have no elements in practice, but with the clock pinned (tests, benchmarks) they may: those are calculated
// again on their own day.
func elementsOfDay[E any](elements []*E, dayNum int, dayOf func(*E) int) []*E {
	return lo.Filter(elements, func(el *E, _ int) bool { return dayOf(el) == dayNum })
}

func dropMatrixElementDay(el *model.DropMatrixElement) int { return el.DayNum }

func (s *DropMatrixElement) ReplaceDay(ctx context.Context, server string, dayNum int, elements []*model.DropMatrixElement) error {
	return s.DropMatrixElementRepo.ReplaceDay(ctx, server, dayNum, elements)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategoryAndDayNumRange(ctx, server, sourceCategory, startDayNum, endDayNum)
}

func (s *DropMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.DropMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}

func (s *DropMatrixElement) CalcGlobalDropMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	return s.DropMatrixElementRepo.CalcGlobalDropMatrixAggregates(ctx, server, targets, sourceCategories)
}

func (s *DropMatrixElement) CalcSiteStatsAggregates(
	ctx context.Context, server string, fromDayNum, toDayNum null.Int,
) ([]*model.SiteStatsAggregate, error) {
	return s.DropMatrixElementRepo.CalcSiteStatsAggregates(ctx, server, fromDayNum, toDayNum)
}
