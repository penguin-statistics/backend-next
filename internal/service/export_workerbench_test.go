//go:build workerbench

package service

import (
	"context"

	"exusiai.dev/backend-next/internal/model"
)

// Exposes the global matrix calculations to the external globalmatrixbench harness.

func (s *DropMatrix) CalcGlobalDropMatrixForBench(ctx context.Context, server, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	return s.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
}

func (s *PatternMatrix) CalcGlobalPatternMatrixForBench(ctx context.Context, server, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	return s.calcGlobalPatternMatrix(ctx, server, sourceCategory)
}

func (s *DropMatrix) CalcGlobalDropMatricesForBench(ctx context.Context, server string, sourceCategories []string) (map[string]*model.DropMatrixQueryResult, error) {
	return s.calcGlobalDropMatrices(ctx, server, sourceCategories)
}

func (s *PatternMatrix) CalcGlobalPatternMatricesForBench(ctx context.Context, server string, sourceCategories []string) (map[string]*model.PatternMatrixQueryResult, error) {
	return s.calcGlobalPatternMatrices(ctx, server, sourceCategories)
}
