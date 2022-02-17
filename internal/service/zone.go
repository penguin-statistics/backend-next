package service

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
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
func (s *ZoneService) GetZones(ctx *fiber.Ctx) ([]*models.Zone, error) {
	var zones []*models.Zone
	err := cache.Zones.Get(&zones)
	if err == nil {
		return zones, nil
	}

	zones, err = s.ZoneRepo.GetZones(ctx.Context())
	go cache.Zones.Set(zones, 24*time.Hour)
	return zones, err
}

func (s *ZoneService) GetZoneById(ctx *fiber.Ctx, id int) (*models.Zone, error) {
	return s.ZoneRepo.GetZoneById(ctx.Context(), id)
}

// Cache: zone#arkZoneId:{arkZoneId}, 24hrs
func (s *ZoneService) GetZoneByArkId(ctx *fiber.Ctx, arkZoneId string) (*models.Zone, error) {
	var zone models.Zone
	err := cache.ZoneByArkId.Get(arkZoneId, &zone)
	if err == nil {
		return &zone, nil
	}

	dbZone, err := s.ZoneRepo.GetZoneByArkId(ctx.Context(), arkZoneId)
	go cache.ZoneByArkId.Set(arkZoneId, dbZone, 24*time.Hour)
	return dbZone, err
}

// Cache: (singular) shimZones, 24hrs; records last modified time
func (s *ZoneService) GetShimZones(ctx *fiber.Ctx) ([]*shims.Zone, error) {
	var zones []*shims.Zone
	err := cache.ShimZones.Get(&zones)
	if err == nil {
		return zones, nil
	}

	zones, err = s.ZoneRepo.GetShimZones(ctx.Context())
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
func (s *ZoneService) GetShimZoneByArkId(ctx *fiber.Ctx, arkZoneId string) (*shims.Zone, error) {
	var zone shims.Zone
	err := cache.ShimItemByArkId.Get(arkZoneId, &zone)
	if err == nil {
		return &zone, nil
	}

	dbZone, err := s.ZoneRepo.GetShimZoneByArkId(ctx.Context(), arkZoneId)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbZone)
	go cache.ShimZoneByArkId.Set(arkZoneId, dbZone, 24*time.Hour)
	return dbZone, nil
}

func (s *ZoneService) applyShim(zone *shims.Zone) {
	zoneNameI18n := gjson.ParseBytes(zone.ZoneNameI18n)
	zone.ZoneName = zoneNameI18n.Map()["zh"].String()

	if zone.Stages != nil {
		for _, stage := range zone.Stages {
			zone.StageIds = append(zone.StageIds, stage.ArkStageID)
		}
	}
}