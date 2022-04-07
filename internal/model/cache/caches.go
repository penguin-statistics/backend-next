package cache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/model"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type Flusher func() error

var (
	ItemDropSetByStageIDAndRangeID   *cache.Set[[]int]
	ItemDropSetByStageIdAndTimeRange *cache.Set[[]int]

	ShimMaxAccumulableDropMatrixResults *cache.Set[modelv2.DropMatrixQueryResult]

	Formula *cache.Singular[json.RawMessage]

	Items           *cache.Singular[[]*model.Item]
	ItemByArkID     *cache.Set[model.Item]
	ShimItems       *cache.Singular[[]*modelv2.Item]
	ShimItemByArkID *cache.Set[modelv2.Item]
	ItemsMapById    *cache.Singular[map[int]*model.Item]
	ItemsMapByArkID *cache.Singular[map[string]*model.Item]

	Notices *cache.Singular[[]*model.Notice]

	ShimActivities *cache.Singular[[]*modelv2.Activity]

	ShimLatestPatternMatrixResults *cache.Set[modelv2.PatternMatrixQueryResult]

	ShimSiteStats *cache.Set[modelv2.SiteStats]

	Stages           *cache.Singular[[]*model.Stage]
	StageByArkID     *cache.Set[model.Stage]
	ShimStages       *cache.Set[[]*modelv2.Stage]
	ShimStageByArkID *cache.Set[modelv2.Stage]
	StagesMapByID    *cache.Singular[map[int]*model.Stage]
	StagesMapByArkID *cache.Singular[map[string]*model.Stage]

	TimeRanges               *cache.Set[[]*model.TimeRange]
	TimeRangeByID            *cache.Set[model.TimeRange]
	TimeRangesMap            *cache.Set[map[int]*model.TimeRange]
	MaxAccumulableTimeRanges *cache.Set[map[int]map[int][]*model.TimeRange]

	ShimSavedTrendResults *cache.Set[modelv2.TrendQueryResult]

	Zones           *cache.Singular[[]*model.Zone]
	ZoneByArkID     *cache.Set[model.Zone]
	ShimZones       *cache.Singular[[]*modelv2.Zone]
	ShimZoneByArkID *cache.Set[modelv2.Zone]

	DropPatternElementsByPatternID *cache.Set[[]*model.DropPatternElement]

	LastModifiedTime *cache.Set[time.Time]

	Properties map[string]string

	once sync.Once

	CacheSetMap             map[string]Flusher
	CacheSingularFlusherMap map[string]Flusher
)

func Initialize(propertyRepo *repo.Property) {
	once.Do(func() {
		initializeCaches()
		populateProperties(propertyRepo)
	})
}

func Delete(name string, key null.String) error {
	if key.Valid {
		if _, ok := CacheSetMap[name]; ok {
			if err := CacheSetMap[name](); err != nil {
				return err
			}
		}
	} else {
		if _, ok := CacheSingularFlusherMap[name]; ok {
			if err := CacheSingularFlusherMap[name](); err != nil {
				return err
			}
		} else if _, ok := CacheSetMap[name]; ok {
			if err := CacheSetMap[name](); err != nil {
				return err
			}
		}
	}
	return nil
}

func initializeCaches() {
	CacheSetMap = make(map[string]Flusher)
	CacheSingularFlusherMap = make(map[string]Flusher)

	// drop_info
	ItemDropSetByStageIDAndRangeID = cache.NewSet[[]int]("itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet[[]int]("itemDropSet#server|stageId|startTime|endTime")

	CacheSetMap["itemDropSet#server|stageId|rangeId"] = ItemDropSetByStageIDAndRangeID.Flush
	CacheSetMap["itemDropSet#server|stageId|startTime|endTime"] = ItemDropSetByStageIdAndTimeRange.Flush

	// drop_matrix
	ShimMaxAccumulableDropMatrixResults = cache.NewSet[modelv2.DropMatrixQueryResult]("shimMaxAccumulableDropMatrixResults#server|showClosedZoned")

	CacheSetMap["shimMaxAccumulableDropMatrixResults#server|showClosedZoned"] = ShimMaxAccumulableDropMatrixResults.Flush

	// formula
	Formula = cache.NewSingular[json.RawMessage]("formula")
	CacheSingularFlusherMap["formula"] = Formula.Delete

	// item
	Items = cache.NewSingular[[]*model.Item]("items")
	ItemByArkID = cache.NewSet[model.Item]("item#arkItemId")
	ShimItems = cache.NewSingular[[]*modelv2.Item]("shimItems")
	ShimItemByArkID = cache.NewSet[modelv2.Item]("shimItem#arkItemId")
	ItemsMapById = cache.NewSingular[map[int]*model.Item]("itemsMapById")
	ItemsMapByArkID = cache.NewSingular[map[string]*model.Item]("itemsMapByArkId")

	CacheSingularFlusherMap["items"] = Items.Delete
	CacheSetMap["item#arkItemId"] = ItemByArkID.Flush
	CacheSingularFlusherMap["shimItems"] = ShimItems.Delete
	CacheSetMap["shimItem#arkItemId"] = ShimItemByArkID.Flush
	CacheSingularFlusherMap["itemsMapById"] = ItemsMapById.Delete
	CacheSingularFlusherMap["itemsMapByArkId"] = ItemsMapByArkID.Delete

	// notice
	Notices = cache.NewSingular[[]*model.Notice]("notices")

	CacheSingularFlusherMap["notices"] = Notices.Delete

	// activity
	ShimActivities = cache.NewSingular[[]*modelv2.Activity]("shimActivities")

	CacheSingularFlusherMap["shimActivities"] = ShimActivities.Delete

	// pattern_matrix
	ShimLatestPatternMatrixResults = cache.NewSet[modelv2.PatternMatrixQueryResult]("shimLatestPatternMatrixResults#server")

	CacheSetMap["shimLatestPatternMatrixResults#server"] = ShimLatestPatternMatrixResults.Flush

	// site_stats
	ShimSiteStats = cache.NewSet[modelv2.SiteStats]("shimSiteStats#server")

	CacheSetMap["shimSiteStats#server"] = ShimSiteStats.Flush

	// stage
	Stages = cache.NewSingular[[]*model.Stage]("stages")
	StageByArkID = cache.NewSet[model.Stage]("stage#arkStageId")
	ShimStages = cache.NewSet[[]*modelv2.Stage]("shimStages#server")
	ShimStageByArkID = cache.NewSet[modelv2.Stage]("shimStage#server|arkStageId")
	StagesMapByID = cache.NewSingular[map[int]*model.Stage]("stagesMapById")
	StagesMapByArkID = cache.NewSingular[map[string]*model.Stage]("stagesMapByArkId")

	CacheSingularFlusherMap["stages"] = Stages.Delete
	CacheSetMap["stage#arkStageId"] = StageByArkID.Flush
	CacheSetMap["shimStages#server"] = ShimStages.Flush
	CacheSetMap["shimStage#server|arkStageId"] = ShimStageByArkID.Flush
	CacheSingularFlusherMap["stagesMapById"] = StagesMapByID.Delete
	CacheSingularFlusherMap["stagesMapByArkId"] = StagesMapByArkID.Delete

	// time_range
	TimeRanges = cache.NewSet[[]*model.TimeRange]("timeRanges#server")
	TimeRangeByID = cache.NewSet[model.TimeRange]("timeRange#rangeId")
	TimeRangesMap = cache.NewSet[map[int]*model.TimeRange]("timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet[map[int]map[int][]*model.TimeRange]("maxAccumulableTimeRanges#server")

	CacheSetMap["timeRanges#server"] = TimeRanges.Flush
	CacheSetMap["timeRange#rangeId"] = TimeRangeByID.Flush
	CacheSetMap["timeRangesMap#server"] = TimeRangesMap.Flush
	CacheSetMap["maxAccumulableTimeRanges#server"] = MaxAccumulableTimeRanges.Flush

	// trend
	ShimSavedTrendResults = cache.NewSet[modelv2.TrendQueryResult]("shimSavedTrendResults#server")

	CacheSetMap["shimSavedTrendResults#server"] = ShimSavedTrendResults.Flush

	// zone
	Zones = cache.NewSingular[[]*model.Zone]("zones")
	ZoneByArkID = cache.NewSet[model.Zone]("zone#arkZoneId")
	ShimZones = cache.NewSingular[[]*modelv2.Zone]("shimZones")
	ShimZoneByArkID = cache.NewSet[modelv2.Zone]("shimZone#arkZoneId")

	CacheSingularFlusherMap["zones"] = Zones.Delete
	CacheSetMap["zone#arkZoneId"] = ZoneByArkID.Flush
	CacheSingularFlusherMap["shimZones"] = ShimZones.Delete
	CacheSetMap["shimZone#arkZoneId"] = ShimZoneByArkID.Flush

	// drop_pattern_elements
	DropPatternElementsByPatternID = cache.NewSet[[]*model.DropPatternElement]("dropPatternElements#patternId")

	CacheSetMap["dropPatternElements#patternId"] = DropPatternElementsByPatternID.Flush

	// others
	LastModifiedTime = cache.NewSet[time.Time]("lastModifiedTime#key")

	CacheSetMap["lastModifiedTime#key"] = LastModifiedTime.Flush
}

func populateProperties(repo *repo.Property) {
	Properties = make(map[string]string)
	properties, err := repo.GetProperties(context.Background())
	if err != nil {
		panic(err)
	}

	for _, property := range properties {
		Properties[property.Key] = property.Value
	}
}
