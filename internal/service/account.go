package service

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *AccountService) GetAccountByPenguinId(ctx *fiber.Ctx, penguinId string) (*models.Account, error) {
	return s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinId)
}

func (s *AccountService) GetAccountFromAuthHeader(ctx *fiber.Ctx, authorization string) (*models.Account, error) {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinID := strings.TrimSpace(strings.TrimPrefix(authorization, "PenguinID"))

	// check PenguinID validity
	var account *models.Account
	var err error
	if penguinID != "" {
		account, err = s.GetAccountByPenguinId(ctx, penguinID)
		if err != nil {
			return nil, err
		}
	}
	return account, nil
}

func (s *AccountService) CreateAccountWithRandomPenguinID(ctx *fiber.Ctx) (*models.Account, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
}
