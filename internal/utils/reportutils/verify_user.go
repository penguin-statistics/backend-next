package reportutils

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

var (
	ErrAccountIDEmpty  = errors.New("account id is empty")
	ErrAccountNotFound = errors.New("account not found with given id")
)

type UserVerifier struct {
	AccountRepo *repos.AccountRepo
}

func NewUserVerifier(accountRepo *repos.AccountRepo) *UserVerifier {
	return &UserVerifier{
		AccountRepo: accountRepo,
	}
}

func (u *UserVerifier) Name() string {
	return "user"
}

func (u *UserVerifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) []error {
	id := reportTask.AccountID
	if id == 0 {
		return []error{ErrAccountIDEmpty}
	}
	if !u.AccountRepo.IsAccountExistWithId(ctx, id) {
		return []error{ErrAccountNotFound}
	}
	return nil
}
