package service

import (
	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *ZoneService) GetZones(ctx *fiber.Ctx) ([]*models.Zone, error) {
	return s.ZoneRepo.GetZones(ctx.Context())
}

func (s *ZoneService) GetZoneByArkId(ctx *fiber.Ctx, arkZoneId string) (*models.Zone, error) {
	return s.ZoneRepo.GetZoneByArkId(ctx.Context(), arkZoneId)
}

func (s *ZoneService) GetShimZones(ctx *fiber.Ctx) ([]*shims.Zone, error) {
	zones, err := s.ZoneRepo.GetShimZones(ctx.Context())
	if err != nil {
		return nil, err
	}
	for _, i := range zones {
		s.applyShim(i)
	}
	return zones, nil
}

func (s *ZoneService) GetShimZoneByArkId(ctx *fiber.Ctx, zoneId string) (*shims.Zone, error) {
	zone, err := s.ZoneRepo.GetShimZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return nil, err
	}
	s.applyShim(zone)
	return zone, nil
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
