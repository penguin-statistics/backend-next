package util

import (
	"exusiai.dev/backend-next/internal/model"
)

// GetDropMatrixElementsMap maps elements by stage id, item id, and range id (or day number without hasRangeID). Of
// elements with the same ids the last one is kept.
func GetDropMatrixElementsMap(elements []*model.DropMatrixElement, hasRangeID bool) map[int]map[int]map[int]*model.DropMatrixElement {
	elementsMap := make(map[int]map[int]map[int]*model.DropMatrixElement)
	for _, element := range elements {
		subMapByItemId, ok := elementsMap[element.StageID]
		if !ok {
			subMapByItemId = make(map[int]map[int]*model.DropMatrixElement)
			elementsMap[element.StageID] = subMapByItemId
		}
		subMapByRangeId, ok := subMapByItemId[element.ItemID]
		if !ok {
			subMapByRangeId = make(map[int]*model.DropMatrixElement)
			subMapByItemId[element.ItemID] = subMapByRangeId
		}
		if hasRangeID {
			subMapByRangeId[element.RangeID] = element
		} else {
			subMapByRangeId[element.DayNum] = element
		}
	}
	return elementsMap
}
