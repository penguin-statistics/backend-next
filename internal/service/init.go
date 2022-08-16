package service

import (
	"context"

	"github.com/jinzhu/copier"
	"github.com/samber/lo"

	"github.com/penguin-statistics/backend-next/internal/model"
	modelv3 "github.com/penguin-statistics/backend-next/internal/model/v3"
)

type Init struct {
	ItemService  *Item
	StageService *Stage
	ZoneService  *Zone
}

func NewInit(itemService *Item, stageService *Stage, zoneService *Zone) *Init {
	return &Init{
		ItemService:  itemService,
		StageService: stageService,
		ZoneService:  zoneService,
	}
}

func (i *Init) GetInit(ctx context.Context) (*modelv3.Init, error) {
	items, err := i.ItemService.GetItems(ctx)
	if err != nil {
		return nil, err
	}
	stages, err := i.StageService.GetStages(ctx)
	if err != nil {
		return nil, err
	}
	zones, err := i.ZoneService.GetZones(ctx)
	if err != nil {
		return nil, err
	}

	castedItems := lo.Map(items, func(in *model.Item, _ int) *modelv3.Item {
		var item modelv3.Item
		err = copier.Copy(&item, in)
		if err != nil {
			panic(err)
		}
		return &item
	})

	castedStages := lo.Map(stages, func(in *model.Stage, _ int) *modelv3.Stage {
		var stage modelv3.Stage
		err = copier.Copy(&stage, in)
		if err != nil {
			panic(err)
		}
		return &stage
	})

	castedZones := lo.Map(zones, func(in *model.Zone, _ int) *modelv3.Zone {
		var zone modelv3.Zone
		err = copier.Copy(&zone, in)
		if err != nil {
			panic(err)
		}
		return &zone
	})

	return &modelv3.Init{
		Items:  castedItems,
		Stages: castedStages,
		Zones:  castedZones,
	}, nil
}
