package service

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *AccountService) GetAccountByPenguinId(ctx *fiber.Ctx, penguinId string) (*models.Account, error) {
	return s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinId)
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

func (s *AccountService) CreateAccountWithRandomPenguinID(ctx *fiber.Ctx) (*models.Account, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
}
