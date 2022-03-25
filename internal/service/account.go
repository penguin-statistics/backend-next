package service

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type AccountService struct {
	AccountRepo *repos.AccountRepo
}

func NewAccountService(accountRepo *repos.AccountRepo) *AccountService {
	return &AccountService{
		AccountRepo: accountRepo,
	}
}

func (s *AccountService) CreateAccountWithRandomPenguinId(ctx context.Context) (*models.Account, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinId(ctx)
}

// Cache: account#accountId:{accountId}, 24hrs
func (s *AccountService) GetAccountById(ctx context.Context, accountId string) (*models.Account, error) {
	var account models.Account
	err := cache.AccountByID.Get(accountId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountById(ctx, accountId)
	if err != nil {
		return nil, err
	}
	go cache.AccountByID.Set(accountId, *dbAccount, time.Hour*24)
	return dbAccount, nil
}

// Cache: account#penguinId:{penguinId}, 24hrs
func (s *AccountService) GetAccountByPenguinId(ctx context.Context, penguinId string) (*models.Account, error) {
	var account models.Account
	err := cache.AccountByPenguinID.Get(penguinId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountByPenguinId(ctx, penguinId)
	if err != nil {
		return nil, err
	}
	go cache.AccountByPenguinID.Set(penguinId, *dbAccount, time.Hour*24)
	return dbAccount, nil
}

func (s *AccountService) IsAccountExistWithId(ctx context.Context, accountId int) bool {
	return s.AccountRepo.IsAccountExistWithId(ctx, accountId)
}

func (s *AccountService) GetAccountFromRequest(ctx *fiber.Ctx) (*models.Account, error) {
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
