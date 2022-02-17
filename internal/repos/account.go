package repos

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

const ACCOUNT_MAX_RETRY = 100

type AccountRepo struct {
	db *bun.DB
}

func NewAccountRepo(db *bun.DB) *AccountRepo {
	return &AccountRepo{db: db}
}

// PenguinID is a 8 number string and padded with 0
func generateRandomPenguinId() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("%08d", rand.Intn(1e8))
}

func (c *AccountRepo) CreateAccountWithRandomPenguinId(ctx context.Context) (*models.Account, error) {
	// retry if account already exists
	for i := 0; i < ACCOUNT_MAX_RETRY; i++ {
		account := &models.Account{
			PenguinID: generateRandomPenguinId(),
		}
		_, err := c.db.NewInsert().
			Model(account).
			Returning("account_id").
			Exec(ctx)
		if err != nil {
			continue
		}
		return account, nil
	}

	return nil, errors.ErrInvalidRequest.WithMessage("failed to create account")
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

func (c *AccountRepo) IsAccountExistWithId(ctx context.Context, accountId int) bool {
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
