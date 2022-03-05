package cache

import (
	"context"
	"sync"

	"github.com/go-redis/redis/v8"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/pkg/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

var (
	AccountById        *cache.Set
	AccountByPenguinId *cache.Set

	ItemDropSetByStageIdAndRangeId   *cache.Set
	ItemDropSetByStageIdAndTimeRange *cache.Set

	ShimMaxAccumulableDropMatrixResults *cache.Set

	Formula *cache.Singular

	Items           *cache.Singular
	ItemByArkId     *cache.Set
	ShimItems       *cache.Singular
	ShimItemByArkId *cache.Set
	ItemsMapById    *cache.Singular
	ItemsMapByArkId *cache.Singular

	Notices *cache.Singular

	Activities     *cache.Singular
	ShimActivities *cache.Singular

	ShimLatestPatternMatrixResults *cache.Set

	ShimSiteStats *cache.Set

	Stages           *cache.Singular
	StageByArkId     *cache.Set
	ShimStages       *cache.Set
	ShimStageByArkId *cache.Set
	StagesMapById    *cache.Singular
	StagesMapByArkId *cache.Singular

	TimeRanges               *cache.Set
	TimeRangeById            *cache.Set
	TimeRangesMap            *cache.Set
	MaxAccumulableTimeRanges *cache.Set

	ShimSavedTrendResults *cache.Set

	RecentReports *cache.Set

	Zones           *cache.Singular
	ZoneByArkId     *cache.Set
	ShimZones       *cache.Singular
	ShimZoneByArkId *cache.Set

	DropPatternElementsByPatternId *cache.Set

	LastModifiedTime *cache.Set

	Properties map[string]string

	once sync.Once

	CacheSetMap      map[string]*cache.Set
	CacheSingularMap map[string]*cache.Singular
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
			if err := CacheSetMap[name].Delete(key.String); err != nil {
				return err
			}
		}
	} else {
		if _, ok := CacheSingularMap[name]; ok {
			if err := CacheSingularMap[name].Delete(); err != nil {
				return err
			}
		} else if _, ok := CacheSetMap[name]; ok {
			if err := CacheSetMap[name].Clear(); err != nil {
				return err
			}
		}
	}
	return nil
}

func initializeCaches(client *redis.Client) {
	CacheSetMap = make(map[string]*cache.Set)
	CacheSingularMap = make(map[string]*cache.Singular)

	// account
	AccountById = cache.NewSet("account#accountId")
	AccountByPenguinId = cache.NewSet("account#penguinId")

	CacheSetMap["account#accountId"] = AccountById
	CacheSetMap["account#penguinId"] = AccountByPenguinId

	// drop_info
	ItemDropSetByStageIdAndRangeId = cache.NewSet("itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet("itemDropSet#server|stageId|startTime|endTime")

	CacheSetMap["itemDropSet#server|stageId|rangeId"] = ItemDropSetByStageIdAndRangeId
	CacheSetMap["itemDropSet#server|stageId|startTime|endTime"] = ItemDropSetByStageIdAndTimeRange

	// drop_matrix
	ShimMaxAccumulableDropMatrixResults = cache.NewSet("shimMaxAccumulableDropMatrixResults#server|showClosedZoned")

	CacheSetMap["shimMaxAccumulableDropMatrixResults#server|showClosedZoned"] = ShimMaxAccumulableDropMatrixResults

	// formula
	Formula = cache.NewSingular("formula")

	CacheSingularMap["formula"] = Formula

	// item
	Items = cache.NewSingular("items")
	ItemByArkId = cache.NewSet("item#arkItemId")
	ShimItems = cache.NewSingular("shimItems")
	ShimItemByArkId = cache.NewSet("shimItem#arkItemId")
	ItemsMapById = cache.NewSingular("itemsMapById")
	ItemsMapByArkId = cache.NewSingular("itemsMapByArkId")

	CacheSingularMap["items"] = Items
	CacheSetMap["item#arkItemId"] = ItemByArkId
	CacheSingularMap["shimItems"] = ShimItems
	CacheSetMap["shimItem#arkItemId"] = ShimItemByArkId
	CacheSingularMap["itemsMapById"] = ItemsMapById
	CacheSingularMap["itemsMapByArkId"] = ItemsMapByArkId

	// notice
	Notices = cache.NewSingular("notices")

	CacheSingularMap["notices"] = Notices

	// activity
	Activities = cache.NewSingular("activities")
	ShimActivities = cache.NewSingular("shimActivities")

	CacheSingularMap["activities"] = Activities
	CacheSingularMap["shimActivities"] = ShimActivities

	// pattern_matrix
	ShimLatestPatternMatrixResults = cache.NewSet("shimLatestPatternMatrixResults#server")

	CacheSetMap["shimLatestPatternMatrixResults#server"] = ShimLatestPatternMatrixResults

	// site_stats
	ShimSiteStats = cache.NewSet("shimSiteStats#server")

	CacheSetMap["shimSiteStats#server"] = ShimSiteStats

	// stage
	Stages = cache.NewSingular("stages")
	StageByArkId = cache.NewSet("stage#arkStageId")
	ShimStages = cache.NewSet("shimStages#server")
	ShimStageByArkId = cache.NewSet("shimStage#server|arkStageId")
	StagesMapById = cache.NewSingular("stagesMapById")
	StagesMapByArkId = cache.NewSingular("stagesMapByArkId")

	CacheSingularMap["stages"] = Stages
	CacheSetMap["stage#arkStageId"] = StageByArkId
	CacheSetMap["shimStages#server"] = ShimStages
	CacheSetMap["shimStage#server|arkStageId"] = ShimStageByArkId
	CacheSingularMap["stagesMapById"] = StagesMapById
	CacheSingularMap["stagesMapByArkId"] = StagesMapByArkId

	// time_range
	TimeRanges = cache.NewSet("timeRanges#server")
	TimeRangeById = cache.NewSet("timeRange#rangeId")
	TimeRangesMap = cache.NewSet("timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet("maxAccumulableTimeRanges#server")

	CacheSetMap["timeRanges#server"] = TimeRanges
	CacheSetMap["timeRange#rangeId"] = TimeRangeById
	CacheSetMap["timeRangesMap#server"] = TimeRangesMap
	CacheSetMap["maxAccumulableTimeRanges#server"] = MaxAccumulableTimeRanges

	// trend
	ShimSavedTrendResults = cache.NewSet("shimSavedTrendResults#server")

	CacheSetMap["shimSavedTrendResults#server"] = ShimSavedTrendResults

	// report
	RecentReports = cache.NewSet("recentReports#recallId")

	CacheSetMap["recentReports#recallId"] = RecentReports

	// zone
	Zones = cache.NewSingular("zones")
	ZoneByArkId = cache.NewSet("zone#arkZoneId")
	ShimZones = cache.NewSingular("shimZones")
	ShimZoneByArkId = cache.NewSet("shimZone#arkZoneId")

	CacheSingularMap["zones"] = Zones
	CacheSetMap["zone#arkZoneId"] = ZoneByArkId
	CacheSingularMap["shimZones"] = ShimZones
	CacheSetMap["shimZone#arkZoneId"] = ShimZoneByArkId

	// drop_pattern_elements
	DropPatternElementsByPatternId = cache.NewSet("dropPatternElements#patternId")

	CacheSetMap["dropPatternElements#patternId"] = DropPatternElementsByPatternId

	// others
	LastModifiedTime = cache.NewSet("lastModifiedTime#key")

	CacheSetMap["lastModifiedTime#key"] = LastModifiedTime
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
