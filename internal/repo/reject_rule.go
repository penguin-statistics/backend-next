package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

const (
	RejectRuleActiveStatus = 1
)

type RejectRule struct {
	DB *bun.DB
}

func NewRejectRule(db *bun.DB) *RejectRule {
	return &RejectRule{DB: db}
}

func (s *RejectRule) GetRejectRule(ctx context.Context, id int) (*model.RejectRule, error) {
	var rejectRule model.RejectRule
	err := s.DB.NewSelect().
		Model(&rejectRule).
		Where("id = ?", id).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &rejectRule, nil
}

func (s *RejectRule) GetAllActiveRejectRules(ctx context.Context) ([]*model.RejectRule, error) {
	var rejectRule []*model.RejectRule
	err := s.DB.NewSelect().
		Model(&rejectRule).
		Where("status = ?", RejectRuleActiveStatus).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return rejectRule, nil
}
