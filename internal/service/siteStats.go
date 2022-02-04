package service

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type SiteStatsService struct {
	DropReportRepo *repos.DropReportRepo
}

func NewSiteStatsService(dropReportRepo *repos.DropReportRepo) *SiteStatsService {
	return &SiteStatsService{
		DropReportRepo: dropReportRepo,
	}
}

func (s *SiteStatsService) GetShimSiteStats(ctx context.Context, server string) (*shims.SiteStats, error) {
	stageTimes, err := s.DropReportRepo.CalcTotalStageQuantityForShimSiteStats(ctx, server, false)
	if err != nil {
		return nil, err
	}

	stageTimes24h, err := s.DropReportRepo.CalcTotalStageQuantityForShimSiteStats(ctx, server, true)
	if err != nil {
		return nil, err
	}

	itemQuantity, err := s.DropReportRepo.CalcTotalItemQuantityForShimSiteStats(ctx, server)
	if err != nil {
		return nil, err
	}

	sanity, err := s.DropReportRepo.CalcTotalSanityCostForShimSiteStats(ctx, server)
	if err != nil {
		return nil, err
	}

	return &shims.SiteStats{
		TotalStageTimes:     stageTimes,
		TotalStageTimes24H:  stageTimes24h,
		TotalItemQuantities: itemQuantity,
		TotalSanityCost:     sanity,
	}, nil
}
