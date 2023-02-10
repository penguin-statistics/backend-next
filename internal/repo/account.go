package repo

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/pkg/pgid"
	"exusiai.dev/backend-next/internal/repo/selector"
)

const AccountMaxRetries = 100

type Account struct {
	db  *bun.DB
	sel selector.S[model.Account]
}

func NewAccount(db *bun.DB) *Account {
	return &Account{
		db:  db,
		sel: selector.New[model.Account](db),
	}
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
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("account_id = ?", accountId)
	})
}

func (c *Account) GetAccountByPenguinId(ctx context.Context, penguinId string) (*model.Account, error) {
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("penguin_id = ?", penguinId)
	})
}

func (c *Account) IsAccountExistWithId(ctx context.Context, accountId int) bool {
	account, err := c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Column("account_id").Where("account_id = ?", accountId)
	})
	if err != nil {
		return false
	}

	return account != nil
}
