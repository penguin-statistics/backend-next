package service

import (
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/convertion"
	"github.com/penguin-statistics/backend-next/internal/models/types"
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

func (s *ReportService) VerifySingularReport(ctx *fiber.Ctx, report *types.SingleReportRequest) error {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinID := strings.TrimSpace(strings.TrimPrefix(ctx.Get("Authorization"), "PenguinID"))

	singleReport := convertion.SingleReportRequestToSingleReport(report)

	reportCtx := &types.ReportContext{
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  report.Server,
			Source:  report.Source,
			Version: report.Version,
		},
		Reports:   []*types.SingleReport{singleReport},
		PenguinID: penguinID,
		IP:        ctx.IP(),
	}

	if err := s.ReportVerifier.Verify(ctx.Context(), reportCtx); err != nil {
		return err
	}

	return ctx.SendStatus(fiber.StatusAccepted)
}

func (s *ReportService) SubmitSingularReport(report *types.BatchReportRequest) {
	spew.Dump(report)
}
