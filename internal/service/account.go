package service

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type Account struct {
	AccountRepo *repo.Account
}

func NewAccount(accountRepo *repo.Account) *Account {
	return &Account{
		AccountRepo: accountRepo,
	}
}

func (s *Account) CreateAccountWithRandomPenguinId(ctx context.Context) (*model.Account, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinId(ctx)
}

// Cache: account#accountId:{accountId}, 1 hr
func (s *Account) GetAccountById(ctx context.Context, accountId string) (*model.Account, error) {
	var account model.Account
	err := cache.AccountByID.Get(accountId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountById(ctx, accountId)
	if err != nil {
		return nil, err
	}
	go cache.AccountByID.Set(accountId, *dbAccount, time.Hour)
	return dbAccount, nil
}

// Cache: account#penguinId:{penguinId}, 1 hr
func (s *Account) GetAccountByPenguinId(ctx context.Context, penguinId string) (*model.Account, error) {
	var account model.Account
	err := cache.AccountByPenguinID.Get(penguinId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountByPenguinId(ctx, penguinId)
	if err != nil {
		return nil, err
	}
	go cache.AccountByPenguinID.Set(penguinId, *dbAccount, time.Hour)
	return dbAccount, nil
}

func (s *Account) IsAccountExistWithId(ctx context.Context, accountId int) bool {
	return s.AccountRepo.IsAccountExistWithId(ctx, accountId)
}

func (s *Account) GetAccountFromRequest(ctx *fiber.Ctx) (*model.Account, error) {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinId := pgid.Extract(ctx)
	if penguinId == "" {
		return nil, pgerr.ErrInvalidReq.Msg("PenguinID not found in request")
	}

	// check PenguinID validity
	account, err := s.GetAccountByPenguinId(ctx.Context(), penguinId)
	if err != nil {
		log.Warn().Str("penguinId", penguinId).Err(err).Msg("failed to get account from request")
		return nil, pgerr.ErrInvalidReq.Msg("PenguinID is invalid")
	}
	return account, nil
}
