package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

const (
	RejectRuleActiveStatus = 1
)

type RejectRule struct {
	db *bun.DB
}

func NewRejectRule(db *bun.DB) *RejectRule {
	return &RejectRule{db: db}
}

func (r *RejectRule) GetRejectRule(ctx context.Context, id int) (*model.RejectRule, error) {
	var rejectRule model.RejectRule
	err := r.db.NewSelect().
		Model(&rejectRule).
		Where("rule_id = ?", id).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &rejectRule, nil
}

func (r *RejectRule) GetAllActiveRejectRules(ctx context.Context) ([]*model.RejectRule, error) {
	var rejectRule []*model.RejectRule
	err := r.db.NewSelect().
		Model(&rejectRule).
		Where("status = ?", RejectRuleActiveStatus).
		Order("rule_id ASC").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return rejectRule, nil
}
