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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	zones := []*models.Zone{objects.Zone}
	if err := s.ZoneRepo.SaveZones(ctx, tx, &zones); err != nil {
		tx.Rollback()
		return err
	}

	//TODO: save other stuff

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return err
	}

	return nil
}
