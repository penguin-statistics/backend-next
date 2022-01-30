package service

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/convertion"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
)

type ReportService struct {
	DropInfoRepo   *repos.DropInfoRepo
	AccountRepo    *repos.AccountRepo
	ReportVerifier *reportutils.ReportVerifier
}

func NewReportService(dropInfoRepo *repos.DropInfoRepo, accountRepo *repos.AccountRepo, reportVerifier *reportutils.ReportVerifier) *ReportService {
	return &ReportService{
		DropInfoRepo:   dropInfoRepo,
		AccountRepo:    accountRepo,
		ReportVerifier: reportVerifier,
	}
}

func (s *ReportService) VerifyAndSubmitSingularReport(ctx *fiber.Ctx, report *types.SingleReportRequest) error {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinID := strings.TrimSpace(strings.TrimPrefix(ctx.Get("Authorization"), "PenguinID"))

	// if PenguinID is empty, create new PenguinID
	account, err := s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinID)
	if err != nil {
		return err
	}
	var accountId int
	if account == nil {
		createdAccount, err := s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
		if err != nil {
			return err
		}
		accountId = createdAccount.AccountID
	} else {
		accountId = account.AccountID
	}

	// merge drops with same (dropType, itemId) pair
	report.Drops = reportutils.MergeDrops(report.Drops)

	singleReport := convertion.SingleReportRequestToSingleReport(report)

	reportCtx := &types.ReportContext{
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  report.Server,
			Source:  report.Source,
			Version: report.Version,
		},
		Reports:   []*types.SingleReport{singleReport},
		AccountID: accountId,
		IP:        ctx.IP(),
	}

	if err := s.ReportVerifier.Verify(ctx.Context(), reportCtx); err != nil {
		return err
	}

	return ctx.SendStatus(fiber.StatusAccepted)
}
