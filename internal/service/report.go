package service

import (
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models/dto"
)

type ReportService struct {
	fx.In
}

func NewReportService() *ReportService {
	return &ReportService{}
}

func (s *ReportService) SubmitSingularReport(report *dto.BatchReportRequest) {

}
