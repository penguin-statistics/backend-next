package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

type Export struct {
	DropReportService         *DropReport
	DropPatternElementService *DropPatternElement
	ItemService               *Item
}

func NewExport(
	dropReportService *DropReport,
	dropPatternElementService *DropPatternElement,
	itemService *Item,
) *Export {
	return &Export{
		DropReportService:         dropReportService,
		DropPatternElementService: dropPatternElementService,
		ItemService:               itemService,
	}
}

func (s *Export) ExportDropReportsAndPatterns(
	ctx context.Context, server string, startTime *time.Time, endTime *time.Time, times null.Int, stageId int, itemIds []int, accountId null.Int, sourceCategory string,
) (*model.ExportDropReportsAndPatternsResult, error) {
	stageIdItemIdMap := make(map[int][]int)
	stageIdItemIdMap[stageId] = itemIds
	queryCtx := &model.DropReportQueryContext{
		Server:             server,
		StartTime:          startTime,
		EndTime:            endTime,
		AccountID:          accountId,
		StageItemFilter:    &stageIdItemIdMap,
		SourceCategory:     sourceCategory,
		ExcludeNonOneTimes: false,
		Times:              times,
	}
	dropReports, err := s.DropReportService.GetDropReports(ctx, queryCtx)
	if err != nil {
		return nil, err
	}

	patternIdsSet := make(map[int]struct{})
	dropReportForExportList := make([]*model.DropReportForExport, 0)
	for _, dropReport := range dropReports {
		dropReportForExportList = append(dropReportForExportList, &model.DropReportForExport{
			Times:      dropReport.Times,
			PatternID:  dropReport.PatternID,
			CreatedAt:  dropReport.CreatedAt.UnixMilli(),
			AccountID:  dropReport.AccountID,
			SourceName: dropReport.SourceName,
			Version:    dropReport.Version,
		})
		patternIdsSet[dropReport.PatternID] = struct{}{}
	}

	patternIds := make([]int, 0)
	for patternId := range patternIdsSet {
		patternIds = append(patternIds, patternId)
	}
	dropPatternElementsMap, err := s.DropPatternElementService.GetDropPatternElementsMapByPatternIds(ctx, patternIds)
	if err != nil {
		return nil, err
	}

	itemsMap, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	dropPatternsForExportList := make([]*model.DropPatternForExport, 0)
	for patternId, dropPatternElements := range dropPatternElementsMap {
		dropPatternElementsForExport := make([]*model.DropPatternElementForExport, 0)
		for _, dropPatternElement := range dropPatternElements {
			dropPatternElementsForExport = append(dropPatternElementsForExport, &model.DropPatternElementForExport{
				ArkItemID: itemsMap[dropPatternElement.ItemID].ArkItemID,
				Quantity:  dropPatternElement.Quantity,
			})
		}
		dropPatternsForExportList = append(dropPatternsForExportList, &model.DropPatternForExport{
			PatternID:           patternId,
			DropPatternElements: dropPatternElementsForExport,
		})
	}

	return &model.ExportDropReportsAndPatternsResult{
		DropReports:  dropReportForExportList,
		DropPatterns: dropPatternsForExportList,
	}, nil
}
