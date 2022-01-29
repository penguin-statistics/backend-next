package service

import (
	"strings"

	"github.com/ahmetb/go-linq/v3"
	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/models/konst"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils/report"
)

type ReportService struct {
	DropInfoRepo   *repos.DropInfoRepo
	ReportVerifier *report.ReportVerifier
}

func NewReportService(dropInfoRepo *repos.DropInfoRepo, reportVerifier *report.ReportVerifier) *ReportService {
	return &ReportService{
		DropInfoRepo:   dropInfoRepo,
		ReportVerifier: reportVerifier,
	}
}

func (s *ReportService) VerifySingularReport(ctx *fiber.Ctx, report *dto.SingleReportRequest) error {
	tuples := make([][]string, 0, len(report.Drops))
	var err error
	linq.From(report.Drops).
		SelectT(func(drop dto.Drop) []string {
			mappedDropType, have := konst.DropTypeMap[drop.DropType]
			if !have {
				err = errors.ErrInvalidRequest.WithMessage("invalid drop type: expected one of %v, but got `%s`", konst.DropTypeMapKeys, drop.DropType)
				return []string{}
			}
			return []string{
				drop.ItemID,
				mappedDropType,
			}
		}).
		ToSlice(&tuples)
	if err != nil {
		return err
	}

	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinID := strings.TrimSpace(strings.TrimPrefix(ctx.Get("Authorization"), "PenguinID"))

	if err = s.ReportVerifier.Verify(ctx.Context(), &dto.SingleReport{
		Report:    report,
		PenguinID: penguinID,
		UserIP:    ctx.IP(),
	}); err != nil {
		return err
	}

	return ctx.SendStatus(fiber.StatusAccepted)
}

func (s *ReportService) SubmitSingularReport(report *dto.BatchReportRequest) {
	spew.Dump(report)
}
