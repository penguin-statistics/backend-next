package service

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/gamedata"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type AdminService struct {
	DB       *bun.DB
	ZoneRepo *repos.ZoneRepo
}

func NewAdminService(db *bun.DB, zoneRepo *repos.ZoneRepo) *AdminService {
	return &AdminService{
		DB:       db,
		ZoneRepo: zoneRepo,
	}
}

func (s *AdminService) SaveRenderedObjects(ctx context.Context, objects *gamedata.RenderedObjects) error {
	var innerErr error
	s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		zones := []*models.Zone{objects.Zone}
		if err := s.ZoneRepo.SaveZones(ctx, tx, &zones); err != nil {
			innerErr = err
			return err
		}

		// TODO: save other stuff

		return nil
	})
	return innerErr
}
