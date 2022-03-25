package cache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type Flusher func() error

var (
	AccountById        *cache.Set[models.Account]
	AccountByPenguinId *cache.Set[models.Account]

	ItemDropSetByStageIdAndRangeId   *cache.Set[[]int]
	ItemDropSetByStageIdAndTimeRange *cache.Set[[]int]

	ShimMaxAccumulableDropMatrixResults *cache.Set[shims.DropMatrixQueryResult]

	Formula *cache.Singular[json.RawMessage]

	Items           *cache.Singular[[]*models.Item]
	ItemByArkId     *cache.Set[models.Item]
	ShimItems       *cache.Singular[[]*shims.Item]
	ShimItemByArkId *cache.Set[shims.Item]
	ItemsMapById    *cache.Singular[map[int]*models.Item]
	ItemsMapByArkId *cache.Singular[map[string]*models.Item]

	Notices *cache.Singular[[]*models.Notice]

	Activities     *cache.Singular[[]*models.Activity]
	ShimActivities *cache.Singular[[]*shims.Activity]

	ShimLatestPatternMatrixResults *cache.Set[shims.PatternMatrixQueryResult]

	ShimSiteStats *cache.Set[shims.SiteStats]

	Stages           *cache.Singular[[]*models.Stage]
	StageByArkId     *cache.Set[models.Stage]
	ShimStages       *cache.Set[[]*shims.Stage]
	ShimStageByArkId *cache.Set[shims.Stage]
	StagesMapById    *cache.Singular[map[int]*models.Stage]
	StagesMapByArkId *cache.Singular[map[string]*models.Stage]

	TimeRanges               *cache.Set[[]*models.TimeRange]
	TimeRangeById            *cache.Set[models.TimeRange]
	TimeRangesMap            *cache.Set[map[int]*models.TimeRange]
	MaxAccumulableTimeRanges *cache.Set[map[int]map[int][]*models.TimeRange]

	ShimSavedTrendResults *cache.Set[shims.TrendQueryResult]

	Zones           *cache.Singular[[]*models.Zone]
	ZoneByArkId     *cache.Set[models.Zone]
	ShimZones       *cache.Singular[[]*shims.Zone]
	ShimZoneByArkId *cache.Set[shims.Zone]

	DropPatternElementsByPatternId *cache.Set[[]*models.DropPatternElement]

	LastModifiedTime *cache.Set[time.Time]

	Properties map[string]string

	once sync.Once

	CacheSetMap             map[string]Flusher
	CacheSingularFlusherMap map[string]Flusher
)

func Initialize(client *redis.Client, propertiesRepo *repos.PropertyRepo) {
	once.Do(func() {
		initializeCaches(client)
		populateProperties(propertiesRepo)
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

func initializeCaches(client *redis.Client) {
	CacheSetMap = make(map[string]Flusher)
	CacheSingularFlusherMap = make(map[string]Flusher)

	// account
	AccountById = cache.NewSet[models.Account]("account#accountId")
	AccountByPenguinId = cache.NewSet[models.Account]("account#penguinId")

	CacheSetMap["account#accountId"] = AccountById.Flush
	CacheSetMap["account#penguinId"] = AccountByPenguinId.Flush

	// drop_info
	ItemDropSetByStageIdAndRangeId = cache.NewSet[[]int]("itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet[[]int]("itemDropSet#server|stageId|startTime|endTime")

	CacheSetMap["itemDropSet#server|stageId|rangeId"] = ItemDropSetByStageIdAndRangeId.Flush
	CacheSetMap["itemDropSet#server|stageId|startTime|endTime"] = ItemDropSetByStageIdAndTimeRange.Flush

	// drop_matrix
	ShimMaxAccumulableDropMatrixResults = cache.NewSet[shims.DropMatrixQueryResult]("shimMaxAccumulableDropMatrixResults#server|showClosedZoned")

	CacheSetMap["shimMaxAccumulableDropMatrixResults#server|showClosedZoned"] = ShimMaxAccumulableDropMatrixResults.Flush

	// formula
	Formula = cache.NewSingular[json.RawMessage]("formula")
	CacheSingularFlusherMap["formula"] = Formula.Delete

	// item
	Items = cache.NewSingular[[]*models.Item]("items")
	ItemByArkId = cache.NewSet[models.Item]("item#arkItemId")
	ShimItems = cache.NewSingular[[]*shims.Item]("shimItems")
	ShimItemByArkId = cache.NewSet[shims.Item]("shimItem#arkItemId")
	ItemsMapById = cache.NewSingular[map[int]*models.Item]("itemsMapById")
	ItemsMapByArkId = cache.NewSingular[map[string]*models.Item]("itemsMapByArkId")

	CacheSingularFlusherMap["items"] = Items.Delete
	CacheSetMap["item#arkItemId"] = ItemByArkId.Flush
	CacheSingularFlusherMap["shimItems"] = ShimItems.Delete
	CacheSetMap["shimItem#arkItemId"] = ShimItemByArkId.Flush
	CacheSingularFlusherMap["itemsMapById"] = ItemsMapById.Delete
	CacheSingularFlusherMap["itemsMapByArkId"] = ItemsMapByArkId.Delete

	// notice
	Notices = cache.NewSingular[[]*models.Notice]("notices")

	CacheSingularFlusherMap["notices"] = Notices.Delete

	// activity
	Activities = cache.NewSingular[[]*models.Activity]("activities")
	ShimActivities = cache.NewSingular[[]*shims.Activity]("shimActivities")

	CacheSingularFlusherMap["activities"] = Activities.Delete
	CacheSingularFlusherMap["shimActivities"] = ShimActivities.Delete

	// pattern_matrix
	ShimLatestPatternMatrixResults = cache.NewSet[shims.PatternMatrixQueryResult]("shimLatestPatternMatrixResults#server")

	CacheSetMap["shimLatestPatternMatrixResults#server"] = ShimLatestPatternMatrixResults.Flush

	// site_stats
	ShimSiteStats = cache.NewSet[shims.SiteStats]("shimSiteStats#server")

	CacheSetMap["shimSiteStats#server"] = ShimSiteStats.Flush

	// stage
	Stages = cache.NewSingular[[]*models.Stage]("stages")
	StageByArkId = cache.NewSet[models.Stage]("stage#arkStageId")
	ShimStages = cache.NewSet[[]*shims.Stage]("shimStages#server")
	ShimStageByArkId = cache.NewSet[shims.Stage]("shimStage#server|arkStageId")
	StagesMapById = cache.NewSingular[map[int]*models.Stage]("stagesMapById")
	StagesMapByArkId = cache.NewSingular[map[string]*models.Stage]("stagesMapByArkId")

	CacheSingularFlusherMap["stages"] = Stages.Delete
	CacheSetMap["stage#arkStageId"] = StageByArkId.Flush
	CacheSetMap["shimStages#server"] = ShimStages.Flush
	CacheSetMap["shimStage#server|arkStageId"] = ShimStageByArkId.Flush
	CacheSingularFlusherMap["stagesMapById"] = StagesMapById.Delete
	CacheSingularFlusherMap["stagesMapByArkId"] = StagesMapByArkId.Delete

	// time_range
	TimeRanges = cache.NewSet[[]*models.TimeRange]("timeRanges#server")
	TimeRangeById = cache.NewSet[models.TimeRange]("timeRange#rangeId")
	TimeRangesMap = cache.NewSet[map[int]*models.TimeRange]("timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet[map[int]map[int][]*models.TimeRange]("maxAccumulableTimeRanges#server")

	CacheSetMap["timeRanges#server"] = TimeRanges.Flush
	CacheSetMap["timeRange#rangeId"] = TimeRangeById.Flush
	CacheSetMap["timeRangesMap#server"] = TimeRangesMap.Flush
	CacheSetMap["maxAccumulableTimeRanges#server"] = MaxAccumulableTimeRanges.Flush

	// trend
	ShimSavedTrendResults = cache.NewSet[shims.TrendQueryResult]("shimSavedTrendResults#server")

	CacheSetMap["shimSavedTrendResults#server"] = ShimSavedTrendResults.Flush

	// zone
	Zones = cache.NewSingular[[]*models.Zone]("zones")
	ZoneByArkId = cache.NewSet[models.Zone]("zone#arkZoneId")
	ShimZones = cache.NewSingular[[]*shims.Zone]("shimZones")
	ShimZoneByArkId = cache.NewSet[shims.Zone]("shimZone#arkZoneId")

	CacheSingularFlusherMap["zones"] = Zones.Delete
	CacheSetMap["zone#arkZoneId"] = ZoneByArkId.Flush
	CacheSingularFlusherMap["shimZones"] = ShimZones.Delete
	CacheSetMap["shimZone#arkZoneId"] = ShimZoneByArkId.Flush

	// drop_pattern_elements
	DropPatternElementsByPatternId = cache.NewSet[[]*models.DropPatternElement]("dropPatternElements#patternId")

	CacheSetMap["dropPatternElements#patternId"] = DropPatternElementsByPatternId.Flush

	// others
	LastModifiedTime = cache.NewSet[time.Time]("lastModifiedTime#key")

	CacheSetMap["lastModifiedTime#key"] = LastModifiedTime.Flush
}

func populateProperties(repo *repos.PropertyRepo) {
	Properties = make(map[string]string)
	properties, err := repo.GetProperties(context.Background())
	if err != nil {
		panic(err)
	}

	for _, property := range properties {
		Properties[property.Key] = property.Value
	}
}
