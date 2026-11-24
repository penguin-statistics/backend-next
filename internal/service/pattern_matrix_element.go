package service

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

type PatternMatrixElement struct {
	PatternMatrixElementRepo *repo.PatternMatrixElement
}

func NewPatternMatrixElement(patternMatrixElementRepo *repo.PatternMatrixElement) *PatternMatrixElement {
	return &PatternMatrixElement{
		PatternMatrixElementRepo: patternMatrixElementRepo,
	}
}

func patternMatrixElementDay(el *model.PatternMatrixElement) int { return el.DayNum }

func (s *PatternMatrixElement) ReplaceDay(ctx context.Context, server string, dayNum int, elements []*model.PatternMatrixElement) error {
	return s.PatternMatrixElementRepo.ReplaceDay(ctx, server, dayNum, elements)
}

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.PatternMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}

func (s *PatternMatrixElement) DB() bun.IDB {
	return s.PatternMatrixElementRepo.DB()
}

func (s *PatternMatrixElement) RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return s.PatternMatrixElementRepo.RunInSnapshotTx(ctx, fn)
}

func (s *PatternMatrixElement) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	return s.PatternMatrixElementRepo.CalcGlobalPatternMatrixAggregates(ctx, db, server, targets, sourceCategories)
}

func (s *PatternMatrixElement) CalcGlobalPatternMatrixPartials(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
	scope model.GlobalMatrixScope,
) (before, after map[string]*model.GlobalPatternMatrixAggregates, err error) {
	return s.PatternMatrixElementRepo.CalcGlobalPatternMatrixPartials(ctx, db, server, targets, sourceCategories, scope)
}
