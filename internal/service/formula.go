package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type FormulaService struct {
	PropertyRepo *repos.PropertyRepo
}

func NewFormulaService(propertyRepo *repos.PropertyRepo) *FormulaService {
	return &FormulaService{
		PropertyRepo: propertyRepo,
	}
}

// Cache: formula, 24hrs
func (s *FormulaService) GetFormula(ctx context.Context) (json.RawMessage, error) {
	var formula json.RawMessage
	err := cache.Formula.Get(&formula)
	if err == nil {
		return formula, nil
	}

	property, err := s.PropertyRepo.GetPropertyByKey(ctx, constants.FormulaPropertyKey)
	if err != nil {
		return nil, err
	}

	msg := json.RawMessage([]byte(property.Value))
	go cache.Formula.Set(msg, 24*time.Hour)

	return msg, nil
}
