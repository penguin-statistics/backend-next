package service

import (
	"context"
	"time"

	"github.com/goccy/go-json"

	"exusiai.dev/backend-next/internal/constant"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

type Formula struct {
	PropertyRepo *repo.Property
}

func NewFormula(propertyRepo *repo.Property) *Formula {
	return &Formula{
		PropertyRepo: propertyRepo,
	}
}

// Cache: (singular) formula, 1 hr
func (s *Formula) GetFormula(ctx context.Context) (json.RawMessage, error) {
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
	cache.Formula.Set(msg, time.Minute*5)

	return msg, nil
}
