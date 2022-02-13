package service

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
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

// TODO: need to implement refresh site stats

// Cache: shimSiteStats#server:{server}, 24hrs
func (s *SiteStatsService) GetShimSiteStats(ctx context.Context, server string) (*shims.SiteStats, error) {
	var results shims.SiteStats
	err := cache.ShimSiteStats.Get(server, &results)
	if err == nil {
		return &results, nil
	}

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

	slowResults := &shims.SiteStats{
		TotalStageTimes:     stageTimes,
		TotalStageTimes24H:  stageTimes24h,
		TotalItemQuantities: itemQuantity,
		TotalSanityCost:     sanity,
	}
	go cache.ShimSiteStats.Set(server, slowResults, 24*time.Hour)
	return slowResults, nil
}
