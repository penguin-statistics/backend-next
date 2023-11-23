package service

import (
	"context"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/dchest/uniuri"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/pkg/pgid"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
	"exusiai.dev/backend-next/internal/util/reportutil"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

var (
	ErrReportNotFound = pgerr.ErrInvalidReq.Msg("report not existed or has already been recalled")
	ErrAccountMissing = pgerr.ErrInvalidReq.Msg("account missing")
	ErrNatsTimeout    = errors.New("timeout waiting for NATS response")
)

type Report struct {
	DB                     *bun.DB
	Redis                  *redis.Client
	NatsJS                 nats.JetStreamContext
	ItemService            *Item
	StageService           *Stage
	AccountService         *Account
	TimeRangeService       *TimeRange
	StageRepo              *repo.Stage
	DropInfoRepo           *repo.DropInfo
	DropReportRepo         *repo.DropReport
	DropPatternRepo        *repo.DropPattern
	DropReportExtraRepo    *repo.DropReportExtra
	DropPatternElementRepo *repo.DropPatternElement
	ReportVerifier         *reportverifs.ReportVerifiers
}

func NewReport(db *bun.DB, redisClient *redis.Client, natsJs nats.JetStreamContext, itemService *Item, stageService *Stage, stageRepo *repo.Stage, dropInfoRepo *repo.DropInfo, dropReportRepo *repo.DropReport, dropReportExtraRepo *repo.DropReportExtra, dropPatternRepo *repo.DropPattern, dropPatternElementRepo *repo.DropPatternElement, accountService *Account, timeRangeService *TimeRange, reportVerifier *reportverifs.ReportVerifiers) *Report {
	service := &Report{
		DB:                     db,
		Redis:                  redisClient,
		NatsJS:                 natsJs,
		ItemService:            itemService,
		StageService:           stageService,
		AccountService:         accountService,
		TimeRangeService:       timeRangeService,
		StageRepo:              stageRepo,
		DropInfoRepo:           dropInfoRepo,
		DropReportRepo:         dropReportRepo,
		DropPatternRepo:        dropPatternRepo,
		DropReportExtraRepo:    dropReportExtraRepo,
		DropPatternElementRepo: dropPatternElementRepo,
		ReportVerifier:         reportVerifier,
	}
	return service
}

func (s *Report) PipelineAccount(ctx *fiber.Ctx) (accountId int, err error) {
	account, err := s.AccountService.GetAccountFromRequest(ctx)
	if err != nil {
		createdAccount, err := s.AccountService.CreateAccountWithRandomPenguinId(ctx.UserContext())
		if err != nil {
			return 0, err
		}
		accountId = createdAccount.AccountID
		pgid.Inject(ctx, createdAccount.PenguinID)
	} else {
		accountId = account.AccountID
	}

	return accountId, nil
}

func (s *Report) PipelinePreprocessRecruitmentTags(ctx context.Context, req *types.SingularReportRequest) error {
	if req.StageID == constant.RecruitStageID {
		recruitTagMap, err := s.ItemService.GetRecruitTagItemsByBilingualName(ctx)
		if err != nil {
			return err
		}

		itemsMap, err := s.ItemService.GetItemsMapByArkId(ctx)
		if err != nil {
			return err
		}

		drops := make([]types.ArkDrop, 0, len(req.Drops))
		for _, v := range req.Drops {
			tag := v.ItemID
			var itemId string
			if _, ok := itemsMap[tag]; ok {
				itemId = tag
			} else if _, ok := recruitTagMap[tag]; ok {
				itemId = recruitTagMap[tag]
			} else {
				return pgerr.ErrInvalidReq.Msg("unexpected recruit tag '%s': not found", tag)
			}
			drops = append(drops, types.ArkDrop{
				DropType: v.DropType,
				ItemID:   itemId,
				Quantity: v.Quantity,
			})
		}

		req.Drops = drops
	}

	return nil
}

func (s *Report) PipelinePreprocessRerunStageIdForMaa(ctx context.Context, req *types.SingularReportRequest) error {
	if !strings.HasSuffix(req.StageID, constant.PermanentStageIdSuffix) {
		return nil
	}
	if req.FragmentReportCommon.Source != constant.MeoAssistant {
		return nil
	}

	// get internal stage id of rerun stage
	originalArkStageId := strings.TrimSuffix(req.StageID, constant.PermanentStageIdSuffix)
	rerunArkStageId := originalArkStageId + constant.RerunStageIdSuffix
	rerunStage, err := s.StageService.GetStageByArkId(ctx, rerunArkStageId)
	if err != nil {
		if !errors.Is(err, pgerr.ErrNotFound) {
			return err
		}
		return nil
	}

	// get latest time range of rerun stage
	timeRangesMap, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, req.Server)
	if err != nil {
		return err
	}
	timeRange, ok := timeRangesMap[rerunStage.StageID]
	if !ok || !timeRange.Includes(time.Now()) {
		return nil
	}

	// if current time is in the latest timerange of rerun stage, use rerun ark stage id
	req.StageID = rerunStage.ArkStageID
	return nil
}

func (s *Report) PipelineMergeDropsAndMapDropTypes(ctx context.Context, drops []types.ArkDrop) ([]*types.Drop, error) {
	convertedDrops := make([]*types.Drop, 0, len(drops))
	for _, drop := range drops {
		item, err := s.ItemService.GetItemByArkId(ctx, drop.ItemID)
		if err != nil {
			if !errors.Is(err, pgerr.ErrNotFound) {
				return nil, err
			} else {
				continue
			}
		}

		convertedDrops = append(convertedDrops, &types.Drop{
			// maps DropType to DB DropType
			DropType: constant.DropTypeMap[drop.DropType],
			ItemID:   item.ItemID,
			Quantity: drop.Quantity,
		})
	}
	convertedDrops = reportutil.MergeDropsByDropTypeAndItemID(convertedDrops)

	return convertedDrops, nil
}

func (s *Report) PipelineTaskId(ctx *fiber.Ctx) string {
	return ctx.Locals(constant.ContextKeyRequestID).(string) + "-" + uniuri.NewLen(16)
}

func (s *Report) PipelineAggregateGachaboxDrops(ctx context.Context, singleReport *types.ReportTaskSingleReport) error {
	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	category, err := s.StageService.GetStageExtraProcessTypeByArkId(ctx, singleReport.StageID)
	if err != nil {
		return err
	}
	if category.Valid && category.String == constant.ExtraProcessTypeGachaBox {
		reportutil.AggregateGachaBoxDrops(singleReport)
	}

	return nil
}

func (s *Report) commitReportTask(ctx *fiber.Ctx, subject string, task *types.ReportTask) (taskId string, err error) {
	taskId = s.PipelineTaskId(ctx)
	task.TaskID = taskId

	reportTaskJsonBytes, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	pub, err := s.NatsJS.PublishAsync(subject, reportTaskJsonBytes)
	if err != nil {
		return "", err
	}

	select {
	case err := <-pub.Err():
		return "", err
	case <-pub.Ok():
		return taskId, nil
	case <-ctx.UserContext().Done():
		return "", ctx.UserContext().Err()
	case <-time.After(time.Second * 10):
		return "", ErrNatsTimeout
	}
}

// returns taskID and error, if any
func (s *Report) PreprocessAndQueueSingularReport(ctx *fiber.Ctx, req *types.SingularReportRequest) (taskId string, err error) {
	accountId, ok := ctx.Locals(constant.LocalsAccountIDKey).(int)
	if !ok {
		return "", ErrAccountMissing
	}

	err = s.PipelinePreprocessRecruitmentTags(ctx.UserContext(), req)
	if err != nil {
		return "", err
	}

	// If stage id is for a perm stage and it's from MAA, we will try to see if the corresponding rerun stage is available or not.
	// If available, we will use the rerun stage id instead. (MAA sometimes uses perm stage id for rerun stages)
	err = s.PipelinePreprocessRerunStageIdForMaa(ctx.UserContext(), req)
	if err != nil {
		return "", err
	}

	// merge drops with same (dropType, itemId) pair
	drops, err := s.PipelineMergeDropsAndMapDropTypes(ctx.UserContext(), req.Drops)
	if err != nil {
		return "", err
	}

	if req.Times == 0 {
		req.Times = 1
	}
	singleReport := &types.ReportTaskSingleReport{
		FragmentStageID: req.FragmentStageID,
		Drops:           drops,
		Times:           req.Times,
		Metadata:        req.Metadata,
	}

	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	err = s.PipelineAggregateGachaboxDrops(ctx.UserContext(), singleReport)
	if err != nil {
		return "", err
	}

	// construct ReportContext
	reportTask := &types.ReportTask{
		CreatedAt: time.Now().UnixMicro(),
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  req.Server,
			Source:  req.Source,
			Version: req.Version,
		},
		Reports:   []*types.ReportTaskSingleReport{singleReport},
		AccountID: accountId,
		IP:        util.ExtractIP(ctx),
	}

	return s.commitReportTask(ctx, "REPORT.SINGLE", reportTask)
}

func (s *Report) PreprocessAndQueueBatchReport(ctx *fiber.Ctx, req *types.BatchReportRequest) (taskId string, err error) {
	accountId, ok := ctx.Locals(constant.LocalsAccountIDKey).(int)
	if !ok {
		return "", ErrAccountMissing
	}

	reports := make([]*types.ReportTaskSingleReport, len(req.BatchDrops))

	for i, drop := range req.BatchDrops {
		// merge drops with same (dropType, itemId) pair
		drops, err := s.PipelineMergeDropsAndMapDropTypes(ctx.UserContext(), drop.Drops)
		if err != nil {
			return "", err
		}

		// catch the variable
		metadata := drop.Metadata
		report := &types.ReportTaskSingleReport{
			FragmentStageID: drop.FragmentStageID,
			Drops:           drops,
			Times:           1,
			Metadata:        &metadata,
		}

		err = s.PipelineAggregateGachaboxDrops(ctx.UserContext(), report)
		if err != nil {
			return "", err
		}

		reports[i] = report
	}

	// construct ReportContext
	reportTask := &types.ReportTask{
		CreatedAt: time.Now().UnixMicro(),
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  req.Server,
			Source:  req.Source,
			Version: req.Version,
		},
		Reports:   reports,
		AccountID: accountId,
		IP:        util.ExtractIP(ctx),
	}

	return s.commitReportTask(ctx, "REPORT.BATCH", reportTask)
}

func (s *Report) RecallSingularReport(ctx context.Context, req *types.SingularReportRecallRequest) error {
	var reportId int
	r := s.Redis.Get(ctx, constant.ReportRedisPrefix+req.ReportHash)

	if errors.Is(r.Err(), redis.Nil) {
		return ErrReportNotFound
	} else if r.Err() != nil {
		return r.Err()
	}

	reportId, err := r.Int()
	if err != nil {
		return err
	}

	err = s.DropReportRepo.DeleteDropReport(ctx, reportId)
	if err != nil {
		return err
	}

	s.Redis.Del(ctx, req.ReportHash)

	return nil
}
