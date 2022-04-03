package repo

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

const AccountMaxRetries = 100

type Account struct {
	db *bun.DB
}

func NewAccount(db *bun.DB) *Account {
	return &Account{db: db}
}

// PenguinID is a 8 number string and padded with 0
func generateRandomPenguinId() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("%08d", rand.Intn(1e8))
}

func (c *Account) CreateAccountWithRandomPenguinId(ctx context.Context) (*models.Account, error) {
	// retry if account already exists
	for i := 0; i < AccountMaxRetries; i++ {
		account := &models.Account{
			PenguinID: generateRandomPenguinId(),
		}
		_, err := c.db.NewInsert().
			Model(account).
			Returning("account_id").
			Exec(ctx)
		if err != nil {
			log.Warn().Err(err).Int("retry", i).Msg("failed to create account. retrying...")
			continue
		} else if i > 0 {
			log.Info().
				Int("retry", i).
				Str("finalizedPenguinID", account.PenguinID).
				Msg("successfully created account after retry")
		}
		return account, nil
	}

	return nil, pgerr.ErrInternalError.Msg("failed to create account")
}

func (c *Account) GetAccountById(ctx context.Context, accountId string) (*models.Account, error) {
	var account models.Account

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

func (c *Account) GetAccountByPenguinId(ctx context.Context, penguinId string) (*models.Account, error) {
	var account models.Account

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
	var account models.Account

	count, err := c.db.NewSelect().
		Model(&account).
		Where("account_id = ?", accountId).
		Count(ctx)
	if err != nil {
		return false
	}

	return count > 0
}
