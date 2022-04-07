package account

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
)

type Service struct {
	AccountRepo *Repo
}

func NewService(accountRepo *Repo) *Service {
	return &Service{
		AccountRepo: accountRepo,
	}
}

func (s *Service) CreateAccountWithRandomPenguinId(ctx context.Context) (*Model, error) {
	return s.AccountRepo.CreateAccountWithRandomPenguinId(ctx)
}

// Cache: account#accountId:{accountId}, 24hrs
func (s *Service) GetAccountById(ctx context.Context, accountId string) (*Model, error) {
	var account Model
	err := CacheByID.Get(accountId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountById(ctx, accountId)
	if err != nil {
		return nil, err
	}
	go CacheByID.Set(accountId, *dbAccount, time.Hour*24)
	return dbAccount, nil
}

// Cache: account#penguinId:{penguinId}, 24hrs
func (s *Service) GetAccountByPenguinId(ctx context.Context, penguinId string) (*Model, error) {
	var account Model
	err := CacheByPenguinID.Get(penguinId, &account)
	if err == nil {
		return &account, nil
	}

	dbAccount, err := s.AccountRepo.GetAccountByPenguinId(ctx, penguinId)
	if err != nil {
		return nil, err
	}
	go CacheByPenguinID.Set(penguinId, *dbAccount, time.Hour*24)
	return dbAccount, nil
}

func (s *Service) IsAccountExistWithId(ctx context.Context, accountId int) bool {
	return s.AccountRepo.IsAccountExistWithId(ctx, accountId)
}

func (s *Service) GetAccountFromRequest(ctx *fiber.Ctx) (*Model, error) {
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
