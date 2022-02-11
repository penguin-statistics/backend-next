package service

import (
	"github.com/gofiber/fiber/v2"

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
	return s.ZoneRepo.GetShimZones(ctx.Context())
}

func (s *ZoneService) GetShimZoneByArkId(ctx *fiber.Ctx, zoneId string) (*shims.Zone, error) {
	return s.ZoneRepo.GetShimZoneByArkId(ctx.Context(), zoneId)
}
