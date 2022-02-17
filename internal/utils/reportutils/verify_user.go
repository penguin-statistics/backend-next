package reportutils

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/models/types"
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

func (u *UserVerifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) error {
	id := reportTask.AccountID
	if id == 0 {
		return errors.New("account id is empty")
	}
	if !u.AccountRepo.IsAccountExistWithId(ctx, id) {
		return errors.New("account not found with given id")
	}
	return nil
}
