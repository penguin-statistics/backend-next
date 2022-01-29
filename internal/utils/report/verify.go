package report

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
)

type ReportVerifier struct {
	UserVerifier *UserVerifier
	DropVerifier *DropVerifier
}

func NewReportVerifier(userVerifier *UserVerifier, dropVerifier *DropVerifier) *ReportVerifier {
	return &ReportVerifier{
		UserVerifier: userVerifier,
		DropVerifier: dropVerifier,
	}
}

func (r *ReportVerifier) Verify(ctx context.Context, report *dto.SingleReport) error {
	if err := r.UserVerifier.Verify(ctx, report); err != nil {
		return err
	}
	if err := r.DropVerifier.Verify(ctx, report); err != nil {
		return err
	}
	return nil
}
