package report

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type UserVerifier struct {
	AccountRepo *repos.AccountRepo
}

func NewUserVerifier(accountRepo *repos.AccountRepo) *UserVerifier {
	return &UserVerifier{
		AccountRepo: accountRepo,
	}
}

func (u *UserVerifier) Verify(ctx context.Context, report *dto.SingleReport) error {
	id := report.PenguinID
	if id == "" {
		return nil
	}
	_, err := u.AccountRepo.GetAccountByPenguinId(ctx, id)
	return err
}
