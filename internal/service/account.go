package service

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type AccountService struct {
	AccountRepo *repos.AccountRepo
}

func NewAccountService(accountRepo *repos.AccountRepo) *AccountService {
	return &AccountService{
		AccountRepo: accountRepo,
	}
}

func (s *AccountService) CreateAccountWithRandomPenguinID(ctx *fiber.Ctx) (*models.Account, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
}

// Cache: AccountById#accountId, 24hrs
func (s *AccountService) GetAccountById(ctx *fiber.Ctx, accountId string) (*models.Account, error) {
	var account *models.Account
	err := cache.AccountById.Get(accountId, account)
	if err == nil {
		return account, nil
	}

	account, err = s.AccountRepo.GetAccountById(ctx.Context(), accountId)
	if err != nil {
		return nil, err
	}
	go cache.AccountById.Set(accountId, account, time.Hour*24)
	return account, nil
}

// Cache: AccountByPenguinId#penguinId, 24hrs
func (s *AccountService) GetAccountByPenguinId(ctx *fiber.Ctx, penguinId string) (*models.Account, error) {
	var account *models.Account
	err := cache.AccountByPenguinId.Get(penguinId, account)
	if err == nil {
		return account, nil
	}

	account, err = s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinId)
	if err != nil {
		return nil, err
	}
	go cache.AccountByPenguinId.Set(penguinId, account, time.Hour*24)
	return account, nil
}

func (s *AccountService) IsAccountExistWithId(ctx *fiber.Ctx, accountId int) bool {
	return s.AccountRepo.IsAccountExistWithId(ctx.Context(), accountId)
}

func (s *AccountService) GetAccountFromRequest(ctx *fiber.Ctx) (*models.Account, error) {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinId := utils.GetPenguinIDFromRequest(ctx)
	if penguinId == "" {
		return nil, errors.New("PenguinID not found in request")
	}

	// check PenguinID validity
	account, err := s.GetAccountByPenguinId(ctx, penguinId)
	if err != nil {
		log.Warn().Str("PenguinID", penguinId).Err(err).Msg("Failed to get account from request")
		return nil, errors.New("PenguinID is invalid")
	}
	return account, nil
}
