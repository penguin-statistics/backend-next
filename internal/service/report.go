package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/models/konst"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type ReportService struct {
	DropInfoRepo *repos.DropInfoRepo
}

func NewReportService(dropInfoRepo *repos.DropInfoRepo) *ReportService {
	return &ReportService{
		DropInfoRepo: dropInfoRepo,
	}
}

func (s *ReportService) VerifySingularReport(ctx *fiber.Ctx, report *dto.SingularReportRequest) error {
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

	itemDropInfos, typeDropInfos, err := s.DropInfoRepo.GetForCurrentTimeRangeWithDropTypes(ctx.Context(), &repos.DropInfoQuery{
		Server:     report.Server,
		ArkStageId: report.StageID,
		DropTuples: tuples,
	})
	if err != nil {
		return err
	}

	if len(itemDropInfos) != len(report.Drops) {
		return errors.ErrInvalidRequest.
			WithMessage("invalid drop info count: expected %d, but got %d", len(report.Drops), len(itemDropInfos))
	}

	// for _, drop := range report.Drops {
	// 	drop.ItemID
	// }

	return ctx.JSON(fiber.Map{
		"item_drop_infos": itemDropInfos,
		"type_drop_infos": typeDropInfos,
	})
}

func (s *ReportService) SubmitSingularReport(report *dto.BatchReportRequest) {
	spew.Dump(report)
}
