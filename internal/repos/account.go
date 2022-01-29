package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type AccountRepo struct {
	db *bun.DB
}

func NewAccountRepo(db *bun.DB) *AccountRepo {
	return &AccountRepo{db: db}
}

func (c *AccountRepo) GetAccountById(ctx context.Context, accountId string) (*models.Account, error) {
	var account models.Account

	err := c.db.NewSelect().
		Model(&account).
		Where("account_id = ?", accountId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &account, nil
}

func (c *AccountRepo) GetAccountByPenguinId(ctx context.Context, penguinId string) (*models.Account, error) {
	var account models.Account

	err := c.db.NewSelect().
		Model(&account).
		Where("penguin_id = ?", penguinId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &account, nil
}
