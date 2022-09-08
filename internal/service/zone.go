package service

import (
	"context"
	"time"

	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type Zone struct {
	ZoneRepo *repo.Zone
}

func NewZone(zoneRepo *repo.Zone) *Zone {
	return &Zone{
		ZoneRepo: zoneRepo,
	}
}

// Cache: (singular) zones, 1 hr
func (s *Zone) GetZones(ctx context.Context) ([]*model.Zone, error) {
	var zones []*model.Zone
	err := cache.Zones.Get(&zones)
	if err == nil {
		return zones, nil
	}

	zones, err = s.ZoneRepo.GetZones(ctx)
	if err != nil {
		return nil, err
	}
	cache.Zones.Set(zones, time.Minute*5)
	return zones, nil
}

func (s *Zone) GetZoneById(ctx context.Context, id int) (*model.Zone, error) {
	return s.ZoneRepo.GetZoneById(ctx, id)
}

// Cache: zone#arkZoneId:{arkZoneId}, 1 hr
func (s *Zone) GetZoneByArkId(ctx context.Context, arkZoneId string) (*model.Zone, error) {
	var zone model.Zone
	err := cache.ZoneByArkID.Get(arkZoneId, &zone)
	if err == nil {
		return &zone, nil
	}

	dbZone, err := s.ZoneRepo.GetZoneByArkId(ctx, arkZoneId)
	if err != nil {
		return nil, err
	}
	cache.ZoneByArkID.Set(arkZoneId, *dbZone, time.Minute*5)
	return dbZone, nil
}

// Cache: (singular) shimZones, 1 hr; records last modified time
func (s *Zone) GetShimZones(ctx context.Context) ([]*modelv2.Zone, error) {
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
	cache.ShimZones.Set(zones, time.Minute*5)
	cache.LastModifiedTime.Set("[shimZones]", time.Now(), 0)
	return zones, nil
}

// Cache: shimZone#arkZoneId:{arkZoneId}, 1 hr
func (s *Zone) GetShimZoneByArkId(ctx context.Context, arkZoneId string) (*modelv2.Zone, error) {
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
	cache.ShimZoneByArkID.Set(arkZoneId, *dbZone, time.Minute*5)
	return dbZone, nil
}

func (s *Zone) applyShim(zone *modelv2.Zone) {
	zoneNameI18n := gjson.ParseBytes(zone.ZoneNameI18n)
	zone.ZoneName = zoneNameI18n.Map()["zh"].String()

	if zone.Stages != nil {
		for _, stage := range zone.Stages {
			zone.StageIds = append(zone.StageIds, stage.ArkStageID)
		}
	}
}
