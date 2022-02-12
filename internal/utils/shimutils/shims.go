package shimutils

import (
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
