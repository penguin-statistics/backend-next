package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type FrontendConfig struct {
	PropertyRepo *repo.Property
}

func NewFrontendConfig(propertyRepo *repo.Property) *FrontendConfig {
	return &FrontendConfig{
		PropertyRepo: propertyRepo,
	}
}

// Cache: (singular) frontend_config, 1 hr
func (s *FrontendConfig) GetFrontendConfig(ctx context.Context) (json.RawMessage, error) {
	var frontendConfig json.RawMessage
	err := cache.FrontendConfig.Get(&frontendConfig)
	if err == nil {
		return frontendConfig, nil
	}

	property, err := s.PropertyRepo.GetPropertyByKey(ctx, constant.FrontendConfigPropertyKey)
	if err != nil {
		return nil, err
	}

	msg := json.RawMessage([]byte(property.Value))
	go cache.FrontendConfig.Set(msg, time.Hour)

	return msg, nil
}
