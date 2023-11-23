package service

import (
	"context"
	"time"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
)

type SiteStats struct {
	DropReportService        *DropReport
	DropMatrixElementService *DropMatrixElement
}

func NewSiteStats(
	dropReportService *DropReport,
	dropMatrixElementService *DropMatrixElement,
) *SiteStats {
	return &SiteStats{
		DropReportService:        dropReportService,
		DropMatrixElementService: dropMatrixElementService,
	}
}

// Cache: shimSiteStats#server:{server}, 24hrs
func (s *SiteStats) GetShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	var results modelv2.SiteStats
	err := cache.ShimSiteStats.Get(server, &results)
	if err == nil {
		return &results, nil
	}

	return s.RefreshShimSiteStats(ctx, server)
}

func (s *SiteStats) RefreshShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	valueFunc := func() (*modelv2.SiteStats, error) {
		stageTimes, err := s.DropMatrixElementService.CalcTotalStageQuantityForShimSiteStats(ctx, server)
		if err != nil {
			return nil, err
		}

		stageTimes24h, err := s.DropReportService.CalcTotalStageQuantityForShimSiteStats(ctx, server, true)
		if err != nil {
			return nil, err
		}

		itemQuantity, err := s.DropMatrixElementService.CalcTotalItemQuantityForShimSiteStats(ctx, server)
		if err != nil {
			return nil, err
		}

		sanity, err := s.DropMatrixElementService.CalcTotalSanityCostForShimSiteStats(ctx, server)
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
