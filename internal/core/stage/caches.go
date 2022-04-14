package stage

import (
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/cache"
)

var (
	Stages           *cache.Singular[[]*Model]
	StageByArkID     *cache.Set[Model]
	ShimStages       *cache.Set[[]*modelv2.Stage]
	ShimStageByArkID *cache.Set[modelv2.Stage]
	StagesMapByID    *cache.Singular[map[int]*Model]
	StagesMapByArkID *cache.Singular[map[string]*Model]
)

func InitCache() {
	Stages = cache.NewSingular[[]*Model]("stages")
	StageByArkID = cache.NewSet[Model]("stage#arkStageId")
	ShimStages = cache.NewSet[[]*modelv2.Stage]("shimStages#server")
	ShimStageByArkID = cache.NewSet[modelv2.Stage]("shimStage#server|arkStageId")
	StagesMapByID = cache.NewSingular[map[int]*Model]("stagesMapById")
	StagesMapByArkID = cache.NewSingular[map[string]*Model]("stagesMapByArkId")
}
