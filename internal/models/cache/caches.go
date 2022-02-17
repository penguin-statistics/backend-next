package cache

import (
	"context"
	"sync"

	"github.com/go-redis/redis/v8"

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

	LastModifiedTime *cache.Set

	Properties map[string]string

	once sync.Once
)

func Initialize(client *redis.Client, propertiesRepo *repos.PropertyRepo) {
	once.Do(func() {
		initializeCaches(client)
		populateProperties(propertiesRepo)
	})
}

func initializeCaches(client *redis.Client) {
	// account
	AccountById = cache.NewSet(client, "account#accountId")
	AccountByPenguinId = cache.NewSet(client, "account#penguinId")

	// drop_info
	ItemDropSetByStageIdAndRangeId = cache.NewSet(client, "itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet(client, "itemDropSet#server|stageId|startTime|endTime")

	// drop_matrix
	ShimMaxAccumulableDropMatrixResults = cache.NewSet(client, "shimMaxAccumulableDropMatrixResults#server|showClosedZoned")

	// formula
	Formula = cache.NewSingular(client, "formula")

	// item
	Items = cache.NewSingular(client, "items")
	ItemByArkId = cache.NewSet(client, "item#arkItemId")
	ShimItems = cache.NewSingular(client, "shimItems")
	ShimItemByArkId = cache.NewSet(client, "shimItem#arkItemId")
	ItemsMapById = cache.NewSingular(client, "itemsMapById")
	ItemsMapByArkId = cache.NewSingular(client, "itemsMapByArkId")

	// notice
	Notices = cache.NewSingular(client, "notices")

	// activity
	Activities = cache.NewSingular(client, "activities")
	ShimActivities = cache.NewSingular(client, "shimActivities")

	// pattern_matrix
	ShimLatestPatternMatrixResults = cache.NewSet(client, "shimLatestPatternMatrixResults#server")

	// site_stats
	ShimSiteStats = cache.NewSet(client, "shimSiteStats#server")

	// stage
	Stages = cache.NewSingular(client, "stages")
	StageByArkId = cache.NewSet(client, "stage#arkStageId")
	ShimStages = cache.NewSet(client, "shimStages#server")
	ShimStageByArkId = cache.NewSet(client, "shimStage#server|arkStageId")
	StagesMapById = cache.NewSingular(client, "stagesMapById")
	StagesMapByArkId = cache.NewSingular(client, "stagesMapByArkId")

	// time_range
	TimeRanges = cache.NewSet(client, "timeRanges#server")
	TimeRangeById = cache.NewSet(client, "timeRange#rangeId")
	TimeRangesMap = cache.NewSet(client, "timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet(client, "maxAccumulableTimeRanges#server")

	// trend
	ShimSavedTrendResults = cache.NewSet(client, "shimSavedTrendResults#server")

	// report
	RecentReports = cache.NewSet(client, "recentReports#recallId")

	// zone
	Zones = cache.NewSingular(client, "zones")
	ZoneByArkId = cache.NewSet(client, "zone#arkZoneId")
	ShimZones = cache.NewSingular(client, "shimZones")
	ShimZoneByArkId = cache.NewSet(client, "shimZone#arkZoneId")

	// others
	LastModifiedTime = cache.NewSet(client, "lastModifiedTime#key")
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
