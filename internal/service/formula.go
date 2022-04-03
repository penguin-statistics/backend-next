package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type FormulaService struct {
	PropertyRepo *repo.Property
}

func NewFormulaService(propertyRepo *repo.Property) *FormulaService {
	return &FormulaService{
		PropertyRepo: propertyRepo,
	}
}

// Cache: (singular) formula, 24hrs
func (s *FormulaService) GetFormula(ctx context.Context) (json.RawMessage, error) {
	var formula json.RawMessage
	err := cache.Formula.Get(&formula)
	if err == nil {
		return formula, nil
	}

	property, err := s.PropertyRepo.GetPropertyByKey(ctx, constant.FormulaPropertyKey)
	if err != nil {
		return nil, err
	}

	msg := json.RawMessage([]byte(property.Value))
	go cache.Formula.Set(msg, 24*time.Hour)

	return msg, nil
}
