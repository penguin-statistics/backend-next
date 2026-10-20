package service

import (
	"context"
	"errors"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

type SiteStats struct {
	DropReportService        *DropReport
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item

	baseGenerations siteStatsBaseGenerations
}

func NewSiteStats(
	dropReportService *DropReport,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
	redisClient *redis.Client,
) *SiteStats {
	return &SiteStats{
		DropReportService:        dropReportService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
		baseGenerations:          redisSiteStatsBaseGenerations{redisClient},
	}
}

// Cache: shimSiteStats#server:{server}, 24hrs
func (s *SiteStats) GetShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	var results modelv2.SiteStats
	err := cache.ShimSiteStats.Get(server, &results)
	if err == nil {
		return &results, nil
	}

	return s.RefreshShimSiteStats(ctx, server)
}

func (s *SiteStats) RefreshShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	valueFunc := func() (*modelv2.SiteStats, error) {
		// the aggregations are independent; run them concurrently
		var (
			stats            modelv2.SiteStats
			baseAggregates   []*model.SiteStatsAggregate
			recentAggregates []*model.SiteStatsAggregate
			stagesMap        map[int]*model.Stage
			itemsMap         map[int]*model.Item
			eg               errgroup.Group
		)
		// only yesterday and today change between worker batches, the days before are aggregated once
		now := util.Now()
		recentFromDayNum := util.GetDayNum(&now, server) - 1
		eg.Go(func() (err error) {
			baseAggregates, err = s.getSiteStatsBaseAggregates(ctx, server, recentFromDayNum)
			return err
		})
		eg.Go(func() (err error) {
			recentAggregates, err = s.DropMatrixElementService.CalcSiteStatsAggregates(ctx, server, null.IntFrom(int64(recentFromDayNum)), null.Int{})
			return err
		})
		eg.Go(func() error {
			stageTimes24h, err := s.DropReportService.CalcTotalStageQuantityForShimSiteStats(ctx, server, true)
			stats.TotalStageTimes24H = stageTimes24h
			return err
		})
		eg.Go(func() (err error) {
			stagesMap, err = s.StageService.GetStagesMapById(ctx)
			return err
		})
		eg.Go(func() (err error) {
			itemsMap, err = s.ItemService.GetItemsMapById(ctx)
			return err
		})
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		aggregates := mergeSiteStatsAggregates(baseAggregates, recentAggregates)
		stats.TotalStageTimes, stats.TotalSanityCost = calcStageTimesForShimSiteStats(aggregates, stagesMap)
		stats.TotalItemQuantities = calcItemQuantitiesForShimSiteStats(aggregates, itemsMap)
		return &stats, nil
	}

	var results modelv2.SiteStats
	cache.ShimSiteStats.Delete(server)
	_, err := cache.ShimSiteStats.MutexGetSet(server, &results, valueFunc, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	cache.LastModifiedTime.Set("[shimSiteStats#server:"+server+"]", time.Now(), 0)
	return &results, nil
}

// Cache: siteStatsBase#server:{server}, 6 hrs, recalculated when the day changes or the base is invalidated
// getSiteStatsBaseAggregates returns the site stats aggregates of every day before beforeDayNum. Those days are
// only rewritten by an admin recalculation, which calls InvalidateSiteStatsBase.
func (s *SiteStats) getSiteStatsBaseAggregates(ctx context.Context, server string, beforeDayNum int) ([]*model.SiteStatsAggregate, error) {
	return s.loadSiteStatsBase(ctx, server, beforeDayNum, func() ([]*model.SiteStatsAggregate, error) {
		return s.DropMatrixElementService.CalcSiteStatsAggregates(ctx, server, null.Int{}, null.IntFrom(int64(beforeDayNum)))
	})
}

func (s *SiteStats) loadSiteStatsBase(
	ctx context.Context, server string, beforeDayNum int, calc func() ([]*model.SiteStatsAggregate, error),
) ([]*model.SiteStatsAggregate, error) {
	// read the generation before calculating: if past days are rewritten meanwhile, the base is cached under an
	// outdated generation and not reused
	generation, err := s.baseGenerations.Get(ctx, server)
	if err != nil {
		return nil, err
	}
	var base model.SiteStatsBase
	if err := cache.SiteStatsBase.Get(server, &base); err == nil && base.BeforeDayNum == beforeDayNum && base.Generation == generation {
		return base.Aggregates, nil
	}
	aggregates, err := calc()
	if err != nil {
		return nil, err
	}
	cache.SiteStatsBase.Set(server, model.SiteStatsBase{BeforeDayNum: beforeDayNum, Generation: generation, Aggregates: aggregates}, 6*time.Hour)
	return aggregates, nil
}

// InvalidateSiteStatsBase must be called after drop matrix elements of a past day of a server have been rewritten:
// every process, including those calculating a base right now, then recalculates its base.
func (s *SiteStats) InvalidateSiteStatsBase(ctx context.Context, server string) error {
	if err := s.baseGenerations.Incr(ctx, server); err != nil {
		return err
	}
	return cache.SiteStatsBase.Delete(server)
}

// siteStatsBaseGenerations counts the invalidations of the site stats base of every server, shared by all
// processes.
type siteStatsBaseGenerations interface {
	Get(ctx context.Context, server string) (int64, error)
	Incr(ctx context.Context, server string) error
}

type redisSiteStatsBaseGenerations struct {
	client *redis.Client
}

func siteStatsBaseGenerationKey(server string) string {
	return "siteStatsBase:generation:" + server
}

func (g redisSiteStatsBaseGenerations) Get(ctx context.Context, server string) (int64, error) {
	generation, err := g.client.Get(ctx, siteStatsBaseGenerationKey(server)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return generation, err
}

func (g redisSiteStatsBaseGenerations) Incr(ctx context.Context, server string) error {
	return g.client.Incr(ctx, siteStatsBaseGenerationKey(server)).Err()
}

// mergeSiteStatsAggregates adds up aggregates of disjoint day ranges per stage and item, without modifying them.
func mergeSiteStatsAggregates(aggregateLists ...[]*model.SiteStatsAggregate) []*model.SiteStatsAggregate {
	type key struct{ stageId, itemId int }
	merged := make(map[key]*model.SiteStatsAggregate)
	results := make([]*model.SiteStatsAggregate, 0)
	for _, aggregates := range aggregateLists {
		for _, aggregate := range aggregates {
			k := key{aggregate.StageID, aggregate.ItemID}
			if m, ok := merged[k]; ok {
				m.Add(aggregate)
				continue
			}
			m := *aggregate
			merged[k] = &m
			results = append(results, &m)
		}
	}
	return results
}

var siteStatsItemTypes = map[string]struct{}{
	constant.ItemTypeMaterial:  {},
	constant.ItemTypeFurniture: {},
	constant.ItemTypeChip:      {},
}

// calcStageTimesForShimSiteStats reports, for every stage except the recruit and extra-process ones, the highest
// total times among its items, and the sanity spent on all of them.
func calcStageTimesForShimSiteStats(aggregates []*model.SiteStatsAggregate, stagesMap map[int]*model.Stage) ([]*modelv2.TotalStageTime, int) {
	stageIds := make([]int, 0)
	maxTimesByStageId := make(map[int]int)
	for _, aggregate := range aggregates {
		if !aggregate.Times.Valid {
			continue
		}
		times, ok := maxTimesByStageId[aggregate.StageID]
		if !ok {
			stageIds = append(stageIds, aggregate.StageID)
		}
		if !ok || int(aggregate.Times.Int64) > times {
			maxTimesByStageId[aggregate.StageID] = int(aggregate.Times.Int64)
		}
	}
	stageTimes := make([]*modelv2.TotalStageTime, 0, len(stageIds))
	sanity := 0
	for _, stageId := range stageIds {
		stage, ok := stagesMap[stageId]
		if !ok || stage.ArkStageID == constant.RecruitStageID || stage.ExtraProcessType.Valid {
			continue
		}
		times := maxTimesByStageId[stageId]
		stageTimes = append(stageTimes, &modelv2.TotalStageTime{StageID: stage.ArkStageID, Times: times})
		sanity += int(stage.Sanity.Int64) * times
	}
	return stageTimes, sanity
}

// calcItemQuantitiesForShimSiteStats sums the quantity of every material, furniture and chip item.
func calcItemQuantitiesForShimSiteStats(aggregates []*model.SiteStatsAggregate, itemsMap map[int]*model.Item) []*modelv2.TotalItemQuantity {
	itemIds := make([]int, 0)
	quantityByItemId := make(map[int]int)
	for _, aggregate := range aggregates {
		if _, ok := quantityByItemId[aggregate.ItemID]; !ok {
			itemIds = append(itemIds, aggregate.ItemID)
		}
		quantityByItemId[aggregate.ItemID] += aggregate.Quantity
	}
	itemQuantities := make([]*modelv2.TotalItemQuantity, 0, len(itemIds))
	for _, itemId := range itemIds {
		item, ok := itemsMap[itemId]
		if !ok {
			continue
		}
		if _, ok := siteStatsItemTypes[item.Type]; !ok {
			continue
		}
		itemQuantities = append(itemQuantities, &modelv2.TotalItemQuantity{ItemID: item.ArkItemID, Quantity: quantityByItemId[itemId]})
	}
	return itemQuantities
}
