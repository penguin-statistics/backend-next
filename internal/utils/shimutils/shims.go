package shimutils

import (
	"strings"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ShimUtil struct {
	StageService              *service.StageService
	ItemService               *service.ItemService
	DropInfoService           *service.DropInfoService
	DropPatternElementService *service.DropPatternElementService
}

func NewShimsUtil(stageService *service.StageService, itemService *service.ItemService, dropInfoService *service.DropInfoService, dropPatternElementService *service.DropPatternElementService) *ShimUtil {
	return &ShimUtil{
		StageService:              stageService,
		ItemService:               itemService,
		DropInfoService:           dropInfoService,
		DropPatternElementService: dropPatternElementService,
	}
}

func (s *ShimUtil) ApplyShimForDropMatrixQuery(ctx *fiber.Ctx, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, queryResult *models.DropMatrixQueryResult) (*shims.DropMatrixQueryResult, error) {
	// get opening stages from dropinfos
	var openingStageIds []int
	if !showClosedZones {
		currentDropInfos, err := s.DropInfoService.GetCurrentDropInfosByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		linq.From(currentDropInfos).SelectT(func(el *models.DropInfo) int { return el.StageID }).Distinct().ToSlice(&openingStageIds)
	}

	// convert comma-splitted stage filter param to a hashset
	stageFilter := make([]string, 0)
	if stageFilterStr != "" {
		stageFilter = strings.Split(stageFilterStr, ",")
	}
	stageFilterSet := make(map[string]struct{}, len(stageFilter))
	for _, stageIdStr := range stageFilter {
		stageFilterSet[stageIdStr] = struct{}{}
	}

	// convert comma-splitted item filter param to a hashset
	itemFilter := make([]string, 0)
	if itemFilterStr != "" {
		itemFilter = strings.Split(itemFilterStr, ",")
	}
	itemFilterSet := make(map[string]struct{}, len(itemFilter))
	for _, itemIdStr := range itemFilter {
		itemFilterSet[itemIdStr] = struct{}{}
	}

	results := &shims.DropMatrixQueryResult{
		Matrix: make([]*shims.OneDropMatrixElement, 0),
	}
	for _, el := range queryResult.Matrix {
		if !showClosedZones && !linq.From(openingStageIds).Contains(el.StageID) {
			continue
		}

		stage, err := s.StageService.GetStageById(ctx, el.StageID)
		if err != nil {
			return nil, err
		}
		if len(stageFilterSet) > 0 {
			if _, ok := stageFilterSet[stage.ArkStageID]; !ok {
				continue
			}
		}

		item, err := s.ItemService.GetItemById(ctx, el.ItemID)
		if err != nil {
			return nil, err
		}
		if len(itemFilterSet) > 0 {
			if _, ok := itemFilterSet[item.ArkItemID]; !ok {
				continue
			}
		}

		endTime := null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		oneDropMatrixElement := shims.OneDropMatrixElement{
			StageID:   stage.ArkStageID,
			ItemID:    item.ArkItemID,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   &endTime,
		}
		if oneDropMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
			oneDropMatrixElement.EndTime = nil
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}

func (s *ShimUtil) ApplyShimForPatternMatrixQuery(ctx *fiber.Ctx, queryResult *models.DropPatternQueryResult) (*shims.PatternMatrixQueryResult, error) {
	results := &shims.PatternMatrixQueryResult{
		PatternMatrix: make([]*shims.OnePatternMatrixElement, 0),
	}
	var groupedResults []linq.Group
	linq.From(queryResult.DropPatterns).
		GroupByT(
			func(el *models.OneDropPattern) int { return el.PatternID },
			func(el *models.OneDropPattern) *models.OneDropPattern { return el },
		).ToSlice(&groupedResults)
	for _, group := range groupedResults {
		patternId := group.Key.(int)
		for _, el := range group.Group {
			oneDropPattern := el.(*models.OneDropPattern)
			stage, err := s.StageService.GetStageById(ctx, oneDropPattern.StageID)
			if err != nil {
				return nil, err
			}
			endTime := null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
			dropPatternElements, err := s.DropPatternElementService.GetDropPatternElementsByPatternId(ctx, patternId)
			if err != nil {
				return nil, err
			}
			// create pattern object from dropPatternElements
			pattern := shims.Pattern{
				Drops: make([]*shims.OneDrop, 0),
			}
			for _, dropPatternElement := range dropPatternElements {
				item, err := s.ItemService.GetItemById(ctx, dropPatternElement.ItemID)
				if err != nil {
					return nil, err
				}
				pattern.Drops = append(pattern.Drops, &shims.OneDrop{
					ItemID:   item.ArkItemID,
					Quantity: dropPatternElement.Quantity,
				})
			}
			onePatternMatrixElement := shims.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   &endTime,
				Pattern:   &pattern,
			}
			if onePatternMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
				onePatternMatrixElement.EndTime = nil
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}

func (s *ShimUtil) ApplyShimForTrendQuery(ctx *fiber.Ctx, queryResult *models.TrendQueryResult) (*shims.TrendQueryResult, error) {
	results := &shims.TrendQueryResult{
		Trend: make(map[string]*shims.StageTrend),
	}
	for _, stageTrend := range queryResult.Trends {
		stage, err := s.StageService.GetStageById(ctx, stageTrend.StageID)
		if err != nil {
			return nil, err
		}
		shimStageTrend := shims.StageTrend{
			Results: make(map[string]*shims.OneItemTrend),
		}
		for _, itemTrend := range stageTrend.Results {
			item, err := s.ItemService.GetItemById(ctx, itemTrend.ItemID)
			if err != nil {
				return nil, err
			}
			shimStageTrend.Results[item.ArkItemID] = &shims.OneItemTrend{
				Quantity:  itemTrend.Quantity,
				Times:     itemTrend.Times,
				StartTime: itemTrend.StartTime.UnixMilli(),
			}
		}
		results.Trend[stage.ArkStageID] = &shimStageTrend
	}
	return results, nil
}
