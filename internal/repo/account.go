package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
)

const AccountMaxRetries = 100

type Account struct {
	db *bun.DB
}

func NewAccount(db *bun.DB) *Account {
	return &Account{db: db}
}

func (c *Account) CreateAccountWithRandomPenguinId(ctx context.Context) (*model.Account, error) {
	// retry if account already exists
	for i := 0; i < AccountMaxRetries; i++ {
		account := &model.Account{
			PenguinID: pgid.New(),
			CreatedAt: time.Now(),
		}

		_, err := c.db.NewInsert().
			Model(account).
			Returning("account_id").
			Exec(ctx)
		if err != nil {
			log.Warn().
				Str("evt.name", "account.create.retry").
				Err(err).
				Int("retry", i).
				Msg("failed to create account. retrying...")
			continue
		}

		if i > 0 {
			log.Info().
				Str("evt.name", "account.create.retry").
				Int("retry", i).
				Str("finalizedPenguinID", account.PenguinID).
				Msg("successfully created account after retry")
		}
		return account, nil
	}

	return nil, pgerr.ErrInternalError.Msg("failed to create account")
}

func (c *Account) GetAccountById(ctx context.Context, accountId string) (*model.Account, error) {
	var account model.Account

	err := c.db.NewSelect().
		Model(&account).
		Column("account_id").
		Where("account_id = ?", accountId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &account, nil
}

func (c *Account) GetAccountByPenguinId(ctx context.Context, penguinId string) (*model.Account, error) {
	var account model.Account

	err := c.db.NewSelect().
		Model(&account).
		Where("penguin_id = ?", penguinId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &account, nil
}

func (c *Account) IsAccountExistWithId(ctx context.Context, accountId int) bool {
	var account model.Account

	err := c.db.NewSelect().
		Model(&account).
		Column("account_id").
		Where("account_id = ?", accountId).
		Scan(ctx, &account)
	if err != nil {
		return false
	}

	return account.AccountID > 0
}
