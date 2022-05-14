package reportverifs

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

const UserViolationReliability = 4

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

func (u *UserVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection {
	id := reportTask.AccountID
	if id == 0 {
		return &Rejection{
			Reliability: UserViolationReliability,
			Message:     ErrAccountIDEmpty.Error(),
		}
	}
	if !u.AccountRepo.IsAccountExistWithId(ctx, id) {
		return &Rejection{
			Reliability: UserViolationReliability,
			Message:     ErrAccountNotFound.Error(),
		}
	}
	return nil
}
