package service

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropPatternElementService struct {
	DropPatternElementRepo *repos.DropPatternElementRepo
}

func NewDropPatternElementService(dropPatternElementRepo *repos.DropPatternElementRepo) *DropPatternElementService {
	return &DropPatternElementService{
		DropPatternElementRepo: dropPatternElementRepo,
	}
}

// Cache: dropPatternElements#patternId:{patternId}, 24hrs
func (s *DropPatternElementService) GetDropPatternElementsByPatternId(ctx *fiber.Ctx, patternId int) ([]*models.DropPatternElement, error) {
	var dropPatternElements []*models.DropPatternElement
	err := cache.DropPatternElementsByPatternId.Get(strconv.Itoa(patternId), &dropPatternElements)
	if err == nil {
		return dropPatternElements, nil
	}

	dbDropPatternElements, err := s.DropPatternElementRepo.GetDropPatternElementsByPatternId(ctx.Context(), patternId)
	if err != nil {
		return nil, err
	}
	go cache.DropPatternElementsByPatternId.Set(strconv.Itoa(patternId), dbDropPatternElements, 24*time.Hour)
	return dbDropPatternElements, nil
}
