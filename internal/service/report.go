package service

import (
	"github.com/davecgh/go-spew/spew"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type ReportService struct {
	itemRepo *repos.ItemRepo
}

func NewReportService(itemRepo *repos.ItemRepo) *ReportService {
	return &ReportService{
		itemRepo: itemRepo,
	}
}

func (s *ReportService) SubmitSingularReport(report *dto.BatchReportRequest) {
	spew.Dump(report)
}
