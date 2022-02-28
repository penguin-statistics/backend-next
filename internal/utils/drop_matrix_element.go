package utils

import (
	"github.com/ahmetb/go-linq/v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

func GetDropMatrixElementsMap(elements []*models.DropMatrixElement) map[int]map[int]map[int]*models.DropMatrixElement {
	elementsMap := make(map[int]map[int]map[int]*models.DropMatrixElement)
	var groupedResults1 []linq.Group
	linq.From(elements).
		GroupByT(
			func(element *models.DropMatrixElement) int { return element.StageID },
			func(element *models.DropMatrixElement) *models.DropMatrixElement { return element },
		).ToSlice(&groupedResults1)
	for _, el := range groupedResults1 {
		stageId := el.Key.(int)
		subMapByItemId := make(map[int]map[int]*models.DropMatrixElement, 0)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el interface{}) int { return el.(*models.DropMatrixElement).ItemID },
				func(el interface{}) *models.DropMatrixElement { return el.(*models.DropMatrixElement) },
			).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			itemId := el2.Key.(int)
			subMapByRangeId := make(map[int]*models.DropMatrixElement)
			for _, el3 := range el2.Group {
				element := el3.(*models.DropMatrixElement)
				subMapByRangeId[element.RangeID] = element
			}
			subMapByItemId[itemId] = subMapByRangeId
		}
		elementsMap[stageId] = subMapByItemId
	}
	return elementsMap
}
