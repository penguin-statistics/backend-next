package util

import (
	"github.com/ahmetb/go-linq/v3"

	"exusiai.dev/backend-next/internal/model"
)

func GetDropMatrixElementsMap(elements []*model.DropMatrixElement, hasRangeID bool) map[int]map[int]map[int]*model.DropMatrixElement {
	elementsMap := make(map[int]map[int]map[int]*model.DropMatrixElement)
	var groupedResults1 []linq.Group
	linq.From(elements).
		GroupByT(
			func(element *model.DropMatrixElement) int { return element.StageID },
			func(element *model.DropMatrixElement) *model.DropMatrixElement { return element },
		).ToSlice(&groupedResults1)
	for _, el := range groupedResults1 {
		stageId := el.Key.(int)
		subMapByItemId := make(map[int]map[int]*model.DropMatrixElement)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el any) int { return el.(*model.DropMatrixElement).ItemID },
				func(el any) *model.DropMatrixElement { return el.(*model.DropMatrixElement) }).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			itemId := el2.Key.(int)
			subMapByRangeId := make(map[int]*model.DropMatrixElement)
			for _, el3 := range el2.Group {
				element := el3.(*model.DropMatrixElement)
				if hasRangeID {
					subMapByRangeId[element.RangeID] = element
				} else {
					subMapByRangeId[element.DayNum] = element
				}
			}
			subMapByItemId[itemId] = subMapByRangeId
		}
		elementsMap[stageId] = subMapByItemId
	}
	return elementsMap
}
