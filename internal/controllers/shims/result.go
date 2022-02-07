package shims

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ResultController struct {
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	AccountService       *service.AccountService
	DropInfoService      *service.DropInfoService
	ItemService          *service.ItemService
	StageService         *service.StageService

	FakeEndTimeMilli int64
}

func RegisterResultController(
	v2 *server.V2,
	dropMatrixService *service.DropMatrixService,
	patternMatrixService *service.PatternMatrixService,
	trendService *service.TrendService,
	accountService *service.AccountService,
	dropInfoService *service.DropInfoService,
	itemService *service.ItemService,
	stageService *service.StageService,
) {
	c := &ResultController{
		DropMatrixService:    dropMatrixService,
		PatternMatrixService: patternMatrixService,
		TrendService:         trendService,
		AccountService:       accountService,
		DropInfoService:      dropInfoService,
		ItemService:          itemService,
		StageService:         stageService,
		FakeEndTimeMilli:     62141368179000,
	}

	v2.Get("/result/matrix", c.GetDropMatrix)
}

func (c *ResultController) GetDropMatrix(ctx *fiber.Ctx) error {
	// TODO: the whole result should be cached, and populated when server starts
	server := ctx.Query("server", "CN")
	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}
	showClosedZones, err := strconv.ParseBool(ctx.Query("show_closed_zones", "false"))
	if err != nil {
		return err
	}
	stageFilterStr := ctx.Query("stageFilter", "")
	itemFilterStr := ctx.Query("itemFilter", "")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromAuthHeader(ctx, ctx.Get("Authorization"))
		if err != nil {
			return err
		}
		if account == nil {
			return fmt.Errorf("account not found")
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	queryResult, err := c.DropMatrixService.GetMaxAccumulableDropMatrixResults(ctx, server, &accountId)
	if err != nil {
		return err
	}

	// get opening stages from dropinfos
	var openingStageIds []int
	if !showClosedZones {
		currentDropInfos, err := c.DropInfoService.GetCurrentDropInfosByServer(ctx, server)
		if err != nil {
			return err
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

	// get id -> stage/item map, to find their ark id
	// TODO: use cache here
	stagesMap, err := c.StageService.GetStagesMap(ctx)
	if err != nil {
		return err
	}
	itemsMap, err := c.ItemService.GetItemsMap(ctx)
	if err != nil {
		return err
	}

	results := &shims.DropMatrixQueryResult{
		Matrix: make([]*shims.OneDropMatrixElement, 0),
	}
	for _, el := range queryResult.Matrix {
		if !showClosedZones && !linq.From(openingStageIds).Contains(el.StageID) {
			continue
		}

		arkStageId := stagesMap[el.StageID].ArkStageID
		if len(stageFilterSet) > 0 {
			if _, ok := stageFilterSet[arkStageId]; !ok {
				continue
			}
		}

		arkItemId := itemsMap[el.ItemID].ArkItemID
		if len(itemFilterSet) > 0 {
			if _, ok := itemFilterSet[arkItemId]; !ok {
				continue
			}
		}

		endTime := null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		oneDropMatrixElement := shims.OneDropMatrixElement{
			StageID:   arkStageId,
			ItemID:    arkItemId,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   &endTime,
		}
		if oneDropMatrixElement.EndTime.Int64 == c.FakeEndTimeMilli {
			oneDropMatrixElement.EndTime = nil
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}

	return ctx.JSON(results)
}
