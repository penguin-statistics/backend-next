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
	AccountById = cache.NewSet(client, "account#accountId")
	AccountByPenguinId = cache.NewSet(client, "account#penguinId")

	CacheSetMap["account#accountId"] = AccountById
	CacheSetMap["account#penguinId"] = AccountByPenguinId

	// drop_info
	ItemDropSetByStageIdAndRangeId = cache.NewSet(client, "itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet(client, "itemDropSet#server|stageId|startTime|endTime")

	CacheSetMap["itemDropSet#server|stageId|rangeId"] = ItemDropSetByStageIdAndRangeId
	CacheSetMap["itemDropSet#server|stageId|startTime|endTime"] = ItemDropSetByStageIdAndTimeRange

	// drop_matrix
	ShimMaxAccumulableDropMatrixResults = cache.NewSet(client, "shimMaxAccumulableDropMatrixResults#server|showClosedZoned")

	CacheSetMap["shimMaxAccumulableDropMatrixResults#server|showClosedZoned"] = ShimMaxAccumulableDropMatrixResults

	// formula
	Formula = cache.NewSingular(client, "formula")

	CacheSingularMap["formula"] = Formula

	// item
	Items = cache.NewSingular(client, "items")
	ItemByArkId = cache.NewSet(client, "item#arkItemId")
	ShimItems = cache.NewSingular(client, "shimItems")
	ShimItemByArkId = cache.NewSet(client, "shimItem#arkItemId")
	ItemsMapById = cache.NewSingular(client, "itemsMapById")
	ItemsMapByArkId = cache.NewSingular(client, "itemsMapByArkId")

	CacheSingularMap["items"] = Items
	CacheSetMap["item#arkItemId"] = ItemByArkId
	CacheSingularMap["shimItems"] = ShimItems
	CacheSetMap["shimItem#arkItemId"] = ShimItemByArkId
	CacheSingularMap["itemsMapById"] = ItemsMapById
	CacheSingularMap["itemsMapByArkId"] = ItemsMapByArkId

	// notice
	Notices = cache.NewSingular(client, "notices")

	CacheSingularMap["notices"] = Notices

	// activity
	Activities = cache.NewSingular(client, "activities")
	ShimActivities = cache.NewSingular(client, "shimActivities")

	CacheSingularMap["activities"] = Activities
	CacheSingularMap["shimActivities"] = ShimActivities

	// pattern_matrix
	ShimLatestPatternMatrixResults = cache.NewSet(client, "shimLatestPatternMatrixResults#server")

	CacheSetMap["shimLatestPatternMatrixResults#server"] = ShimLatestPatternMatrixResults

	// site_stats
	ShimSiteStats = cache.NewSet(client, "shimSiteStats#server")

	CacheSetMap["shimSiteStats#server"] = ShimSiteStats

	// stage
	Stages = cache.NewSingular(client, "stages")
	StageByArkId = cache.NewSet(client, "stage#arkStageId")
	ShimStages = cache.NewSet(client, "shimStages#server")
	ShimStageByArkId = cache.NewSet(client, "shimStage#server|arkStageId")
	StagesMapById = cache.NewSingular(client, "stagesMapById")
	StagesMapByArkId = cache.NewSingular(client, "stagesMapByArkId")

	CacheSingularMap["stages"] = Stages
	CacheSetMap["stage#arkStageId"] = StageByArkId
	CacheSetMap["shimStages#server"] = ShimStages
	CacheSetMap["shimStage#server|arkStageId"] = ShimStageByArkId
	CacheSingularMap["stagesMapById"] = StagesMapById
	CacheSingularMap["stagesMapByArkId"] = StagesMapByArkId

	// time_range
	TimeRanges = cache.NewSet(client, "timeRanges#server")
	TimeRangeById = cache.NewSet(client, "timeRange#rangeId")
	TimeRangesMap = cache.NewSet(client, "timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet(client, "maxAccumulableTimeRanges#server")

	CacheSetMap["timeRanges#server"] = TimeRanges
	CacheSetMap["timeRange#rangeId"] = TimeRangeById
	CacheSetMap["timeRangesMap#server"] = TimeRangesMap
	CacheSetMap["maxAccumulableTimeRanges#server"] = MaxAccumulableTimeRanges

	// trend
	ShimSavedTrendResults = cache.NewSet(client, "shimSavedTrendResults#server")

	CacheSetMap["shimSavedTrendResults#server"] = ShimSavedTrendResults

	// report
	RecentReports = cache.NewSet(client, "recentReports#recallId")

	CacheSetMap["recentReports#recallId"] = RecentReports

	// zone
	Zones = cache.NewSingular(client, "zones")
	ZoneByArkId = cache.NewSet(client, "zone#arkZoneId")
	ShimZones = cache.NewSingular(client, "shimZones")
	ShimZoneByArkId = cache.NewSet(client, "shimZone#arkZoneId")

	CacheSingularMap["zones"] = Zones
	CacheSetMap["zone#arkZoneId"] = ZoneByArkId
	CacheSingularMap["shimZones"] = ShimZones
	CacheSetMap["shimZone#arkZoneId"] = ShimZoneByArkId

	// drop_pattern_elements
	DropPatternElementsByPatternId = cache.NewSet(client, "dropPatternElements#patternId")

	CacheSetMap["dropPatternElements#patternId"] = DropPatternElementsByPatternId

	// others
	LastModifiedTime = cache.NewSet(client, "lastModifiedTime#key")

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
