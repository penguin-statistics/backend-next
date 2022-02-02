package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropMatrixElementService struct {
	DropMatrixElementRepo *repos.DropMatrixElementRepo
}

func NewDropMatrixElementService(dropMatrixElementRepo *repos.DropMatrixElementRepo) *DropMatrixElementService {
	return &DropMatrixElementService{
		DropMatrixElementRepo: dropMatrixElementRepo,
	}
}

func (s *DropMatrixElementService) BatchSaveElements(ctx *fiber.Ctx, elements []models.DropMatrixElement, server string) error {
	return s.DropMatrixElementRepo.BatchSaveElements(ctx.Context(), elements, server)
}

func (s *DropMatrixElementService) DeleteByServer(ctx *fiber.Ctx, server string) error {
	return s.DropMatrixElementRepo.DeleteByServer(ctx.Context(), server)
}

func (s *DropMatrixElementService) GetElementsMapByServer(ctx *fiber.Ctx, server string) (map[int] map[int] map[int] *models.DropMatrixElement, error) {
	elements, err := s.DropMatrixElementRepo.GetElementsByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	elementsMap := make(map[int] map[int] map[int] *models.DropMatrixElement, 0)
	var groupedResults1 []linq.Group
	linq.From(elements).
		GroupByT(
			func (element *models.DropMatrixElement) int { return element.StageID }, 
			func (element *models.DropMatrixElement) *models.DropMatrixElement { return element },
		).ToSlice(&groupedResults1)
	for _, el := range groupedResults1 {
		stageId := el.Key.(int)
		subMapByItemId := make(map[int] map[int] *models.DropMatrixElement, 0)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func (el interface{}) int { return el.(*models.DropMatrixElement).ItemID }, 
				func (el interface{}) *models.DropMatrixElement { return el.(*models.DropMatrixElement) },
			).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			itemId := el2.Key.(int)
			subMapByRangeId := make(map[int] *models.DropMatrixElement, 0)
			for _, el3 := range el2.Group {
				element := el3.(*models.DropMatrixElement)
				subMapByRangeId[element.RangeID] = element
			}
			subMapByItemId[itemId] = subMapByRangeId
		}
		elementsMap[stageId] = subMapByItemId
	}
	return elementsMap, nil
}
