package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *DropPatternElementService) GetDropPatternElementsMapByPatternIds(ctx *fiber.Ctx, patternIds []int) (map[int][]*models.DropPatternElement, error) {
	elements, err := s.DropPatternElementRepo.GetDropPatternElementsByPatternIds(ctx.Context(), patternIds)
	if err != nil {
		return nil, err
	}
	results := make(map[int][]*models.DropPatternElement)
	for _, element := range elements {
		if _, ok := results[element.DropPatternID]; !ok {
			results[element.DropPatternID] = make([]*models.DropPatternElement, 0)
		}
		results[element.DropPatternID] = append(results[element.DropPatternID], element)
	}
	return results, nil
}
