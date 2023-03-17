package cache

import (
	"context"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cache"
	"exusiai.dev/backend-next/internal/repo"
)

type Flusher func() error

var (
	AccountByID        *cache.Set[model.Account]
	AccountByPenguinID *cache.Set[model.Account]

	ItemDropSetByStageIDAndRangeID   *cache.Set[[]int]
	ItemDropSetByStageIdAndTimeRange *cache.Set[[]int]

	ShimGlobalDropMatrix *cache.Set[modelv2.DropMatrixQueryResult]

	ShimTrend *cache.Set[modelv2.TrendQueryResult]

	ShimGlobalPatternMatrix *cache.Set[modelv2.PatternMatrixQueryResult]

	Formula *cache.Singular[json.RawMessage]

	FrontendConfig *cache.Singular[json.RawMessage]

	Items           *cache.Singular[[]*model.Item]
	ItemByArkID     *cache.Set[model.Item]
	ShimItems       *cache.Singular[[]*modelv2.Item]
	ShimItemByArkID *cache.Set[modelv2.Item]
	ItemsMapById    *cache.Singular[map[int]*model.Item]
	ItemsMapByArkID *cache.Singular[map[string]*model.Item]

	RecruitTagMap *cache.Singular[map[string]string]

	Notices *cache.Singular[[]*model.Notice]

	Activities     *cache.Singular[[]*model.Activity]
	ShimActivities *cache.Singular[[]*modelv2.Activity]

	ShimSiteStats *cache.Set[modelv2.SiteStats]

	Stages           *cache.Singular[[]*model.Stage]
	StageByArkID     *cache.Set[model.Stage]
	ShimStages       *cache.Set[[]*modelv2.Stage]
	ShimStageByArkID *cache.Set[modelv2.Stage]
	StagesMapByID    *cache.Singular[map[int]*model.Stage]
	StagesMapByArkID *cache.Singular[map[string]*model.Stage]

	TimeRanges                  *cache.Set[[]*model.TimeRange]
	TimeRangeByID               *cache.Set[model.TimeRange]
	TimeRangesMap               *cache.Set[map[int]*model.TimeRange]
	MaxAccumulableTimeRanges    *cache.Set[map[int]map[int][]*model.TimeRange]
	AllMaxAccumulableTimeRanges *cache.Set[map[int]map[int][]*model.TimeRange]
	LatestTimeRanges            *cache.Set[map[int]*model.TimeRange]

	Zones           *cache.Singular[[]*model.Zone]
	ZoneByArkID     *cache.Set[model.Zone]
	ShimZones       *cache.Singular[[]*modelv2.Zone]
	ShimZoneByArkID *cache.Set[modelv2.Zone]

	DropPatternElementsByPatternID *cache.Set[[]*model.DropPatternElement]

	LastModifiedTime *cache.Set[time.Time]

	Properties map[string]string

	once sync.Once

	SetMap             map[string]Flusher
	SingularFlusherMap map[string]Flusher
)

func Initialize(propertyRepo *repo.Property) {
	once.Do(func() {
		initializeCaches()
		populateProperties(propertyRepo)
	})
}

func Delete(name string, key null.String) error {
	if key.Valid {
		if _, ok := SetMap[name]; ok {
			if err := SetMap[name](); err != nil {
				return err
			}
		}
	} else {
		if _, ok := SingularFlusherMap[name]; ok {
			if err := SingularFlusherMap[name](); err != nil {
				return err
			}
		} else if _, ok := SetMap[name]; ok {
			if err := SetMap[name](); err != nil {
				return err
			}
		}
	}
	return nil
}

func initializeCaches() {
	SetMap = make(map[string]Flusher)
	SingularFlusherMap = make(map[string]Flusher)

	// account
	AccountByID = cache.NewSet[model.Account]("account#accountId")
	AccountByPenguinID = cache.NewSet[model.Account]("account#penguinId")

	SetMap["account#accountId"] = AccountByID.Flush
	SetMap["account#penguinId"] = AccountByPenguinID.Flush

	// drop_info
	ItemDropSetByStageIDAndRangeID = cache.NewSet[[]int]("itemDropSet#server|stageId|rangeId")
	ItemDropSetByStageIdAndTimeRange = cache.NewSet[[]int]("itemDropSet#server|stageId|startTime|endTime")

	SetMap["itemDropSet#server|stageId|rangeId"] = ItemDropSetByStageIDAndRangeID.Flush
	SetMap["itemDropSet#server|stageId|startTime|endTime"] = ItemDropSetByStageIdAndTimeRange.Flush

	// drop_matrix
	ShimGlobalDropMatrix = cache.NewSet[modelv2.DropMatrixQueryResult]("shimGlobalDropMatrix#server|showClosedZoned|sourceCategory")

	SetMap["shimGlobalDropMatrix#server|showClosedZoned|sourceCategory"] = ShimGlobalDropMatrix.Flush

	// trend
	ShimTrend = cache.NewSet[modelv2.TrendQueryResult]("shimTrend#server")

	SetMap["shimTrend#server"] = ShimTrend.Flush

	// pattern_matrix
	ShimGlobalPatternMatrix = cache.NewSet[modelv2.PatternMatrixQueryResult]("shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns")

	SetMap["shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns"] = ShimGlobalPatternMatrix.Flush

	// formula
	Formula = cache.NewSingular[json.RawMessage]("formula")
	SingularFlusherMap["formula"] = Formula.Delete

	// frontend_config
	FrontendConfig = cache.NewSingular[json.RawMessage]("frontendConfig")
	SingularFlusherMap["frontendConfig"] = FrontendConfig.Delete

	// item
	Items = cache.NewSingular[[]*model.Item]("items")
	ItemByArkID = cache.NewSet[model.Item]("item#arkItemId")
	ShimItems = cache.NewSingular[[]*modelv2.Item]("shimItems")
	ShimItemByArkID = cache.NewSet[modelv2.Item]("shimItem#arkItemId")
	ItemsMapById = cache.NewSingular[map[int]*model.Item]("itemsMapById")
	ItemsMapByArkID = cache.NewSingular[map[string]*model.Item]("itemsMapByArkId")

	SingularFlusherMap["items"] = Items.Delete
	SetMap["item#arkItemId"] = ItemByArkID.Flush
	SingularFlusherMap["shimItems"] = ShimItems.Delete
	SetMap["shimItem#arkItemId"] = ShimItemByArkID.Flush
	SingularFlusherMap["itemsMapById"] = ItemsMapById.Delete
	SingularFlusherMap["itemsMapByArkId"] = ItemsMapByArkID.Delete

	// recruit tag maps (for report)
	RecruitTagMap = cache.NewSingular[map[string]string]("recruitTagMap#bilingualTagName")
	SingularFlusherMap["recruitTagMap#bilingualTagName"] = RecruitTagMap.Delete

	// notice
	Notices = cache.NewSingular[[]*model.Notice]("notices")

	SingularFlusherMap["notices"] = Notices.Delete

	// activity
	Activities = cache.NewSingular[[]*model.Activity]("activities")
	ShimActivities = cache.NewSingular[[]*modelv2.Activity]("shimActivities")

	SingularFlusherMap["activities"] = Activities.Delete
	SingularFlusherMap["shimActivities"] = ShimActivities.Delete

	// site_stats
	ShimSiteStats = cache.NewSet[modelv2.SiteStats]("shimSiteStats#server")

	SetMap["shimSiteStats#server"] = ShimSiteStats.Flush

	// stage
	Stages = cache.NewSingular[[]*model.Stage]("stages")
	StageByArkID = cache.NewSet[model.Stage]("stage#arkStageId")
	ShimStages = cache.NewSet[[]*modelv2.Stage]("shimStages#server")
	ShimStageByArkID = cache.NewSet[modelv2.Stage]("shimStage#server|arkStageId")
	StagesMapByID = cache.NewSingular[map[int]*model.Stage]("stagesMapById")
	StagesMapByArkID = cache.NewSingular[map[string]*model.Stage]("stagesMapByArkId")

	SingularFlusherMap["stages"] = Stages.Delete
	SetMap["stage#arkStageId"] = StageByArkID.Flush
	SetMap["shimStages#server"] = ShimStages.Flush
	SetMap["shimStage#server|arkStageId"] = ShimStageByArkID.Flush
	SingularFlusherMap["stagesMapById"] = StagesMapByID.Delete
	SingularFlusherMap["stagesMapByArkId"] = StagesMapByArkID.Delete

	// time_range
	TimeRanges = cache.NewSet[[]*model.TimeRange]("timeRanges#server")
	TimeRangeByID = cache.NewSet[model.TimeRange]("timeRange#rangeId")
	TimeRangesMap = cache.NewSet[map[int]*model.TimeRange]("timeRangesMap#server")
	MaxAccumulableTimeRanges = cache.NewSet[map[int]map[int][]*model.TimeRange]("maxAccumulableTimeRanges#server")
	AllMaxAccumulableTimeRanges = cache.NewSet[map[int]map[int][]*model.TimeRange]("allMaxAccumulableTimeRanges#server")
	LatestTimeRanges = cache.NewSet[map[int]*model.TimeRange]("latestTimeRanges#server")

	SetMap["timeRanges#server"] = TimeRanges.Flush
	SetMap["timeRange#rangeId"] = TimeRangeByID.Flush
	SetMap["timeRangesMap#server"] = TimeRangesMap.Flush
	SetMap["maxAccumulableTimeRanges#server"] = MaxAccumulableTimeRanges.Flush
	SetMap["allMaxAccumulableTimeRanges#server"] = AllMaxAccumulableTimeRanges.Flush
	SetMap["latestTimeRanges#server"] = LatestTimeRanges.Flush

	// zone
	Zones = cache.NewSingular[[]*model.Zone]("zones")
	ZoneByArkID = cache.NewSet[model.Zone]("zone#arkZoneId")
	ShimZones = cache.NewSingular[[]*modelv2.Zone]("shimZones")
	ShimZoneByArkID = cache.NewSet[modelv2.Zone]("shimZone#arkZoneId")

	SingularFlusherMap["zones"] = Zones.Delete
	SetMap["zone#arkZoneId"] = ZoneByArkID.Flush
	SingularFlusherMap["shimZones"] = ShimZones.Delete
	SetMap["shimZone#arkZoneId"] = ShimZoneByArkID.Flush

	// drop_pattern_elements
	DropPatternElementsByPatternID = cache.NewSet[[]*model.DropPatternElement]("dropPatternElements#patternId")

	SetMap["dropPatternElements#patternId"] = DropPatternElementsByPatternID.Flush

	// others
	LastModifiedTime = cache.NewSet[time.Time]("lastModifiedTime#key")

	SetMap["lastModifiedTime#key"] = LastModifiedTime.Flush
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
