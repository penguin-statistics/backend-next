package reportverifs

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

var ErrExprMatched = errors.New("blocklist expr matched")

type RejectRuleVerifier struct {
	DropInfoRepo *repo.DropInfo
}

// ensure RejectRuleVerifier conforms to Verifier
var _ Verifier = (*RejectRuleVerifier)(nil)

func NewRejectRuleVerifier(dropInfoRepo *repo.DropInfo) *RejectRuleVerifier {
	return &RejectRuleVerifier{
		DropInfoRepo: dropInfoRepo,
	}
}

func (d *RejectRuleVerifier) Name() string {
	return "reject_rule"
}

func (d *RejectRuleVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) (errs []error) {
	return []error{}
}
