package service

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type SiteStatsService struct {
	DropReportRepo *repo.DropReport
}

func NewSiteStatsService(dropReportRepo *repo.DropReport) *SiteStatsService {
	return &SiteStatsService{
		DropReportRepo: dropReportRepo,
	}
}

// Cache: shimSiteStats#server:{server}, 24hrs
func (s *SiteStatsService) GetShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	var results modelv2.SiteStats
	err := cache.ShimSiteStats.Get(server, &results)
	if err == nil {
		return &results, nil
	}

	return s.RefreshShimSiteStats(ctx, server)
}

func (s *SiteStatsService) RefreshShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	valueFunc := func() (*modelv2.SiteStats, error) {
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

		return &modelv2.SiteStats{
			TotalStageTimes:     stageTimes,
			TotalStageTimes24H:  stageTimes24h,
			TotalItemQuantities: itemQuantity,
			TotalSanityCost:     sanity,
		}, nil
	}

	var results modelv2.SiteStats
	cache.ShimSiteStats.Delete(server)
	_, err := cache.ShimSiteStats.MutexGetSet(server, &results, valueFunc, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	cache.LastModifiedTime.Set("[shimSiteStats#server:"+server+"]", time.Now(), 0)
	return &results, nil
}
