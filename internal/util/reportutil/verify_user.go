package reportutil

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

var (
	ErrAccountIDEmpty  = errors.New("account id is empty")
	ErrAccountNotFound = errors.New("account not found with given id")
)

type UserVerifier struct {
	AccountRepo *repo.Account
}

// ensure UserVerifier conforms to Verifier
var _ Verifier = (*UserVerifier)(nil)

func NewUserVerifier(accountRepo *repo.Account) *UserVerifier {
	return &UserVerifier{
		AccountRepo: accountRepo,
	}
}

func (u *UserVerifier) Name() string {
	return "user"
}

func (u *UserVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) []error {
	id := reportTask.AccountID
	if id == 0 {
		return []error{ErrAccountIDEmpty}
	}
	if !u.AccountRepo.IsAccountExistWithId(ctx, id) {
		return []error{ErrAccountNotFound}
	}
	return nil
}
