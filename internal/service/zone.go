package service

import (
	"context"
	"time"

	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type ZoneService struct {
	ZoneRepo *repos.ZoneRepo
}

func NewZoneService(zoneRepo *repos.ZoneRepo) *ZoneService {
	return &ZoneService{
		ZoneRepo: zoneRepo,
	}
}

// Cache: (singular) zones, 24hrs
func (s *ZoneService) GetZones(ctx context.Context) ([]*models.Zone, error) {
	var zones []*models.Zone
	err := cache.Zones.Get(&zones)
	if err == nil {
		return zones, nil
	}

	zones, err = s.ZoneRepo.GetZones(ctx)
	if err != nil {
		return nil, err
	}
	go cache.Zones.Set(zones, 24*time.Hour)
	return zones, nil
}

func (s *ZoneService) GetZoneById(ctx context.Context, id int) (*models.Zone, error) {
	return s.ZoneRepo.GetZoneById(ctx, id)
}

// Cache: zone#arkZoneId:{arkZoneId}, 24hrs
func (s *ZoneService) GetZoneByArkId(ctx context.Context, arkZoneId string) (*models.Zone, error) {
	var zone models.Zone
	err := cache.ZoneByArkID.Get(arkZoneId, &zone)
	if err == nil {
		return &zone, nil
	}

	dbZone, err := s.ZoneRepo.GetZoneByArkId(ctx, arkZoneId)
	if err != nil {
		return nil, err
	}
	go cache.ZoneByArkID.Set(arkZoneId, *dbZone, 24*time.Hour)
	return dbZone, nil
}

// Cache: (singular) shimZones, 24hrs; records last modified time
func (s *ZoneService) GetShimZones(ctx context.Context) ([]*modelv2.Zone, error) {
	var zones []*modelv2.Zone
	err := cache.ShimZones.Get(&zones)
	if err == nil {
		return zones, nil
	}

	zones, err = s.ZoneRepo.GetShimZones(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range zones {
		s.applyShim(i)
	}
	if err := cache.ShimZones.Set(zones, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[shimZones]", time.Now(), 0)
	}
	return zones, nil
}

// Cache: shimZone#arkZoneId:{arkZoneId}, 24hrs
func (s *ZoneService) GetShimZoneByArkId(ctx context.Context, arkZoneId string) (*modelv2.Zone, error) {
	var zone modelv2.Zone
	err := cache.ShimZoneByArkID.Get(arkZoneId, &zone)
	if err == nil {
		return &zone, nil
	}

	dbZone, err := s.ZoneRepo.GetShimZoneByArkId(ctx, arkZoneId)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbZone)
	go cache.ShimZoneByArkID.Set(arkZoneId, *dbZone, 24*time.Hour)
	return dbZone, nil
}

func (s *ZoneService) applyShim(zone *modelv2.Zone) {
	zoneNameI18n := gjson.ParseBytes(zone.ZoneNameI18n)
	zone.ZoneName = zoneNameI18n.Map()["zh"].String()

	if zone.Stages != nil {
		for _, stage := range zone.Stages {
			zone.StageIds = append(zone.StageIds, stage.ArkStageID)
		}
	}
}
