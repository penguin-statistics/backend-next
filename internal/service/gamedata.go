package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/gamedata"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type GamedataService struct {
	ItemService *ItemService
}

func NewGamedataService(itemService *ItemService) *GamedataService {
	return &GamedataService{
		ItemService: itemService,
	}
}

var dropTypeOrderMapping = map[string]int{
	"REGULAR":          0,
	"SPECIAL":          1,
	"EXTRA":            2,
	"FURNITURE":        3,
	"RECOGNITION_ONLY": 4,
}

func (s *GamedataService) UpdateBrandNewEvent(ctx context.Context, context *gamedata.BrandNewEventContext) (*gamedata.BrandNewEventObjects, error) {
	zone, err := s.renderNewZone(context)
	if err != nil {
		return nil, err
	}

	timeRange, err := s.renderNewTimeRange(context)
	if err != nil {
		return nil, err
	}

	activity, err := s.renderNewActivity(context)
	if err != nil {
		return nil, err
	}

	importStages, err := s.fetchLatestStages([]string{context.ArkZoneID})
	if err != nil {
		return nil, err
	}

	stages := make([]*models.Stage, 0)
	dropInfos := make([]*models.DropInfo, 0)
	for _, gamedataStage := range importStages {
		stage, dropInfosForOneStage, err := s.genStageAndDropInfosFromGameData(ctx, context.Server, gamedataStage, 0, nil)
		if err != nil {
			return nil, err
		}
		stages = append(stages, stage)
		dropInfos = append(dropInfos, dropInfosForOneStage...)
	}

	return &gamedata.BrandNewEventObjects{
		Zone:      zone,
		Stages:    stages,
		DropInfos: dropInfos,
		TimeRange: timeRange,
		Activity:  activity,
	}, nil
}

func (s *GamedataService) renderNewZone(context *gamedata.BrandNewEventContext) (*models.Zone, error) {
	nameMap := make(map[string]string)
	for _, lang := range constants.Languages {
		nameMap[lang] = context.ZoneName
	}
	name, err := json.Marshal(nameMap)
	if err != nil {
		return nil, err
	}

	existenceMap := make(map[string]map[string]interface{})
	for _, s := range constants.Servers {
		existenceMap[s] = map[string]interface{}{
			"exist": false,
		}
		if s == context.Server {
			existenceMap[s]["exist"] = true
			existenceMap[s]["openTime"] = context.StartTime.UnixMilli()
			fakeEndTime := time.UnixMilli(constants.FakeEndTimeMilli)
			endTime := &fakeEndTime
			if context.EndTime != nil {
				endTime = context.EndTime
			}
			existenceMap[s]["closeTime"] = endTime.UnixMilli()
		}
	}
	existence, err := json.Marshal(existenceMap)
	if err != nil {
		return nil, err
	}

	backgroundStr := constants.ZoneBackgroundPath + context.ArkZoneID + constants.ZoneBackgroundExtension
	background := null.StringFrom(backgroundStr)

	return &models.Zone{
		ArkZoneID:  context.ArkZoneID,
		Index:      0,
		Category:   context.ZoneCategory,
		Type:       context.ZoneType,
		Name:       name,
		Existence:  existence,
		Background: &background,
	}, nil
}

func (s *GamedataService) renderNewTimeRange(context *gamedata.BrandNewEventContext) (*models.TimeRange, error) {
	fakeEndTime := time.UnixMilli(constants.FakeEndTimeMilli)
	endTime := &fakeEndTime
	if context.EndTime != nil {
		endTime = context.EndTime
	}

	name := null.StringFrom(utils.GetZonePrefixFromArkZoneID(context.ArkZoneID) + "_" + context.Server)
	startTimeInComment := context.StartTime.In(constants.LocMap[context.Server]).Format("2006/1/02 15:04")
	endTimeInComment := "?"
	if context.EndTime != nil {
		endTimeInComment = context.EndTime.In(constants.LocMap[context.Server]).Format("2006/1/02 15:04")
	}
	comment := null.StringFrom(constants.ServerNameMapping[context.Server] + context.ZoneName + " " + startTimeInComment + " - " + endTimeInComment)
	return &models.TimeRange{
		StartTime: context.StartTime,
		EndTime:   endTime,
		Server:    context.Server,
		Name:      &name,
		Comment:   &comment,
	}, nil
}

func (s *GamedataService) renderNewActivity(context *gamedata.BrandNewEventContext) (*models.Activity, error) {
	fakeEndTime := time.UnixMilli(constants.FakeEndTimeMilli)
	endTime := &fakeEndTime
	if context.EndTime != nil {
		endTime = context.EndTime
	}

	nameMap := make(map[string]string)
	for _, lang := range constants.Languages {
		nameMap[lang] = context.ZoneName
	}
	name, err := json.Marshal(nameMap)
	if err != nil {
		return nil, err
	}

	existenceMap := make(map[string]map[string]interface{})
	for _, s := range constants.Servers {
		existenceMap[s] = map[string]interface{}{
			"exist": false,
		}
		if s == context.Server {
			existenceMap[s]["exist"] = true
		}
	}
	existence, err := json.Marshal(existenceMap)
	if err != nil {
		return nil, err
	}

	return &models.Activity{
		StartTime: context.StartTime,
		EndTime:   endTime,
		Name:      name,
		Existence: existence,
	}, nil
}

func (s *GamedataService) fetchLatestStages(arkZoneIds []string) ([]*gamedata.Stage, error) {
	res, err := http.Get("https://raw.githubusercontent.com/Kengxxiao/ArknightsGameData/master/zh_CN/gamedata/excel/stage_table.json")
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get")
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	stageTable := gamedata.StageTable{}

	json.Unmarshal([]byte(body), &stageTable)

	importStages := make([]*gamedata.Stage, 0)
	for _, stage := range stageTable.Stages {
		if len(arkZoneIds) > 0 && !linq.From(arkZoneIds).Contains(stage.ZoneID) {
			continue
		}

		if utils.IsCampaignStage(stage) {

		} else if utils.IsGuideStage(stage) {

		} else if utils.IsDailyStage(stage) {

		} else if utils.IsChallengeModeStage(stage) {

		} else if utils.IsTrainingStage(stage) {

		} else if utils.IsStoryStage(stage) {

		} else if utils.IsNormalModeExStage(stage) {

		} else {
			importStages = append(importStages, stage)
		}
	}
	linq.From(importStages).
		DistinctByT(func(stage *gamedata.Stage) string { return stage.StageID }).
		SortT(func(a, b *gamedata.Stage) bool { return utils.CompareStageCode(a.Code, b.Code) }).
		ToSlice(&importStages)
	return importStages, nil
}

func (s *GamedataService) genStageAndDropInfosFromGameData(ctx context.Context, server string, gamedataStage *gamedata.Stage, zoneId int, timeRange *models.TimeRange) (*models.Stage, []*models.DropInfo, error) {
	codeMap := make(map[string]string)
	for _, lang := range constants.Languages {
		codeMap[lang] = gamedataStage.Code
	}
	code, err := json.Marshal(codeMap)
	if err != nil {
		return nil, nil, err
	}

	existenceMap := make(map[string]map[string]bool)
	for _, s := range constants.Servers {
		exist := false
		if s == server {
			exist = true
		}
		existenceMap[s] = map[string]bool{
			"exist": exist,
		}
	}
	existence, err := json.Marshal(existenceMap)
	if err != nil {
		return nil, nil, err
	}

	stage := &models.Stage{
		ArkStageID: gamedataStage.StageID,
		ZoneID:     zoneId,
		StageType:  gamedataStage.StageType,
		Sanity:     null.IntFrom(int64(gamedataStage.ApCost)),
		Code:       code,
		Existence:  existence,
	}

	itemsMap, err := s.ItemService.GetItemsMapByArkId(ctx)
	if err != nil {
		return nil, nil, err
	}
	var activityToken string
	for _, reward := range gamedataStage.StageDropInfo.DisplayDetailRewards {
		if reward.Type == constants.ItemTypeActivity && activityToken == "" {
			activityToken = reward.Id
			break
		}
	}

	extrasMap := map[string]string{}
	if activityToken != "" {
		extrasMap["arkItemId"] = activityToken
	}

	groupedRewards := make(map[int][]*gamedata.DisplayDetailReward)
	groupedRewards[2] = make([]*gamedata.DisplayDetailReward, 0)
	groupedRewards[3] = make([]*gamedata.DisplayDetailReward, 0)
	groupedRewards[4] = make([]*gamedata.DisplayDetailReward, 0)
	for _, reward := range gamedataStage.StageDropInfo.DisplayDetailRewards {
		if reward.DropType > 4 || reward.DropType < 2 {
			continue
		}
		groupedRewards[reward.DropType] = append(groupedRewards[reward.DropType], reward)
	}

	dropInfos := make([]*models.DropInfo, 0)
	for dropType, rewards := range groupedRewards {
		items := make([]*models.Item, 0)
		for _, reward := range rewards {
			if reward.Type == constants.ItemTypeMaterial {
				item := itemsMap[reward.Id]
				items = append(items, item)
				bounds := s.decideItemBounds(item, gamedataStage.ApCost)
				dropInfos = append(dropInfos, &models.DropInfo{
					Server:      server,
					ItemID:      null.IntFrom(int64(item.ItemID)),
					DropType:    utils.RewardTypeMap[dropType],
					Accumulable: true,
					Bounds:      bounds,
				})
			}
		}

		// add dropinfo for dropType
		dropTypeBounds := s.decideDropTypeBounds(dropType, items, gamedataStage.ApCost)
		dropInfos = append(dropInfos, &models.DropInfo{
			Server:      server,
			DropType:    utils.RewardTypeMap[dropType],
			Accumulable: true,
			Bounds:      dropTypeBounds,
		})
	}

	// add dropinfo for furniture
	if gamedataStage.ApCost != 0 {
		item := itemsMap[constants.FurnitureArkItemID]
		dropInfos = append(dropInfos, &models.DropInfo{
			Server:      server,
			ItemID:      null.IntFrom(int64(item.ItemID)),
			DropType:    constants.DropTypeFurniture,
			Accumulable: true,
			Bounds:      &models.Bounds{Upper: 1, Lower: 0},
		})
	}

	// add dropinfo for recognition only item
	if activityToken != "" {
		extras, err := json.Marshal(extrasMap)
		if err != nil {
			return nil, nil, err
		}
		dropInfos = append(dropInfos, &models.DropInfo{
			Server:      server,
			DropType:    constants.DropTypeRecognitionOnly,
			Accumulable: false,
			Extras:      extras,
		})
	}

	linq.From(dropInfos).SortT(func(a, b *models.DropInfo) bool {
		if a.ItemID.Valid && b.ItemID.Valid || !a.ItemID.Valid && !b.ItemID.Valid {
			if a.DropType == b.DropType {
				return a.ItemID.Int64 < b.ItemID.Int64
			} else {
				return dropTypeOrderMapping[a.DropType] < dropTypeOrderMapping[b.DropType]
			}
		} else {
			return a.ItemID.Valid
		}
	}).ToSlice(&dropInfos)

	return stage, dropInfos, nil
}

func (s *GamedataService) decideItemBounds(item *models.Item, sanity int) *models.Bounds {
	var upper int
	var lower int
	if item.Rarity >= 2 {
		upper = 1
		lower = 0
	} else if item.Rarity == 1 {
		upper = 3
		lower = 0
	} else if item.Rarity == 0 {
		upper = 5
		lower = 0
	}
	bounds := &models.Bounds{
		Upper: upper,
		Lower: lower,
	}
	return bounds
}

func (s *GamedataService) decideDropTypeBounds(dropType int, items []*models.Item, sanity int) *models.Bounds {
	if dropType == 2 || dropType == 3 {
		return &models.Bounds{Upper: len(items), Lower: 0}
	}
	if dropType == 4 {
		if len(items) == 0 {
			return &models.Bounds{Upper: 0, Lower: 0}
		} else {
			return &models.Bounds{Upper: 1, Lower: 0}
		}
	}
	return nil
}
