package service

import (
	"context"
	"errors"
	"sync"
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

	// publications holds a *siteStatsPublication per server
	publications sync.Map
}

// siteStatsPublication orders the caching of the site stats of a server. Its calculations (the refreshes of the
// worker and the admin endpoint, and that of requests on a cold cache) run concurrently, and one is cached only if no
// calculation that started after it has been cached already: stats calculated from older matrix elements thus never
// replace stats calculated from newer ones.
type siteStatsPublication struct {
	mu sync.Mutex
	// started numbers the calculations in the order they start; published is the number of the latest-started one
	// cached
	started, published uint64
}

func (s *SiteStats) publication(server string) *siteStatsPublication {
	p, _ := s.publications.LoadOrStore(server, &siteStatsPublication{})
	return p.(*siteStatsPublication)
}

// start numbers a calculation, which must read nothing before.
func (p *siteStatsPublication) start() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started++
	return p.started
}

// publish caches the stats of calculation seq by calling cacheValue, unless a calculation started after seq has been
// cached. cacheValue reports whether it cached them, or found no newer ones cached either (a calculation that started
// before seq then skips publication too). cacheValue may take the lock of the last modified times and that of the
// cache, never the other way around.
func (p *siteStatsPublication) publish(seq uint64, cacheValue func() bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if seq < p.published {
		return
	}
	if cacheValue() {
		p.published = seq
	}
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

// Cache: shimSiteStats#server:{server}, 24hrs, records last modified time
func (s *SiteStats) GetShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	p := s.publication(server)
	var seq uint64
	valueFunc := func() (*modelv2.SiteStats, error) {
		seq = p.start()
		return s.calcShimSiteStats(ctx, server)
	}
	// the stats a request calculates are cached through p too, so that a refresh that started before them does not
	// replace them. Should p skip them, MutexGetSet does not cache them either when it calls cacheValue itself: the
	// refresh that started after seq and was cached wrote the key after valueFunc was called, which MutexGetSet notices.
	lastModified := cache.PublishingLastModifiedTime[modelv2.SiteStats]("[shimSiteStats#server:" + server + "]")
	publish := func(value *modelv2.SiteStats, cacheValue func() bool) {
		p.publish(seq, func() (cached bool) {
			lastModified(value, func() bool {
				cached = cacheValue()
				return cached
			})
			return cached
		})
	}

	var results modelv2.SiteStats
	if _, err := cache.ShimSiteStats.MutexGetSetPublishing(server, &results, valueFunc, 24*time.Hour, publish); err != nil {
		return nil, err
	}
	return &results, nil
}

// RefreshShimSiteStats recalculates the site stats of server and replaces the cached ones, unless a calculation that
// started later (another refresh, or that of a request on a cold cache) has cached its stats already; it returns the
// stats it calculated. Requests keep getting the previous ones in the meantime instead of waiting for a
// recalculation. If the recalculation fails, the cached ones are dropped, also if it panics.
func (s *SiteStats) RefreshShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
	refreshed := false
	defer func() {
		if !refreshed {
			cache.ShimSiteStats.Delete(server)
		}
	}()
	p := s.publication(server)
	seq := p.start()
	results, err := s.calcShimSiteStats(ctx, server)
	if err != nil {
		return nil, err
	}
	p.publish(seq, func() bool {
		cache.SetLastModifiedTime("[shimSiteStats#server:"+server+"]", *results, func() {
			cache.ShimSiteStats.Set(server, *results, 24*time.Hour)
		})
		return true
	})
	refreshed = true
	return results, nil
}

func (s *SiteStats) calcShimSiteStats(ctx context.Context, server string) (*modelv2.SiteStats, error) {
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
