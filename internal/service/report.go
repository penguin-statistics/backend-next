package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dchest/uniuri"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/util"
	"github.com/penguin-statistics/backend-next/internal/util/reportutil"
	"github.com/penguin-statistics/backend-next/internal/util/reportverifs"
)

var (
	ErrReportNotFound = pgerr.ErrInvalidReq.Msg("report not existed or has already been recalled")
	ErrNatsTimeout    = errors.New("timeout waiting for NATS response")
)

type Report struct {
	DB                     *bun.DB
	Redis                  *redis.Client
	NatsJS                 nats.JetStreamContext
	ItemService            *Item
	StageService           *Stage
	AccountService         *Account
	StageRepo              *repo.Stage
	DropInfoRepo           *repo.DropInfo
	DropReportRepo         *repo.DropReport
	DropPatternRepo        *repo.DropPattern
	DropReportExtraRepo    *repo.DropReportExtra
	DropPatternElementRepo *repo.DropPatternElement
	ReportVerifier         *reportverifs.ReportVerifiers
}

func NewReport(db *bun.DB, redisClient *redis.Client, natsJs nats.JetStreamContext, itemService *Item, stageService *Stage, stageRepo *repo.Stage, dropInfoRepo *repo.DropInfo, dropReportRepo *repo.DropReport, dropReportExtraRepo *repo.DropReportExtra, dropPatternRepo *repo.DropPattern, dropPatternElementRepo *repo.DropPatternElement, accountService *Account, reportVerifier *reportverifs.ReportVerifiers) *Report {
	service := &Report{
		DB:                     db,
		Redis:                  redisClient,
		NatsJS:                 natsJs,
		ItemService:            itemService,
		StageService:           stageService,
		AccountService:         accountService,
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

func (s *Report) pipelineAccount(ctx *fiber.Ctx) (accountId int, err error) {
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

func (s *Report) pipelinePreprocessRecruitmentTags(ctx context.Context, req *types.SingleReportRequest) error {
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

func (s *Report) pipelineConvertLegacySuppliesForMaa(ctx context.Context, req *types.SingleReportRequest) error {
	if time.Now().UnixMilli() <= 1666641600000 && req.FragmentReportCommon.Source == constant.MeoAssistant {
		for i := range req.Drops {
			if req.Drops[i].ItemID == "randomMaterial_5" {
				req.Drops[i].ItemID = "randomMaterial_7"
				break
			}
		}
	}
	return nil
}

func (s *Report) pipelineMergeDropsAndMapDropTypes(ctx context.Context, drops []types.ArkDrop) ([]*types.Drop, error) {
	convertedDrops := make([]*types.Drop, 0, len(drops))
	for _, drop := range drops {
		item, err := s.ItemService.GetItemByArkId(ctx, drop.ItemID)
		if err != nil {
			if !errors.Is(err, pgerr.ErrNotFound) {
				return nil, err
			} else {
				log.Warn().Msgf("failed to get item by ark id '%s', will ignore it", drop.ItemID)
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

	drops = reportutil.MergeDropsByDropTypeAndItemID(drops)

	return convertedDrops, nil
}

func (s *Report) pipelineTaskId(ctx *fiber.Ctx) string {
	return ctx.Locals(constant.ContextKeyRequestID).(string) + "-" + uniuri.NewLen(16)
}

func (s *Report) pipelineAggregateGachaboxDrops(ctx context.Context, singleReport *types.ReportTaskSingleReport) error {
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
	taskId = s.pipelineTaskId(ctx)
	task.TaskID = taskId

	reportTaskJSON, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	pub, err := s.NatsJS.PublishAsync(subject, reportTaskJSON)
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
func (s *Report) PreprocessAndQueueSingularReport(ctx *fiber.Ctx, req *types.SingleReportRequest) (taskId string, err error) {
	// if account is not found, create new account
	accountId, err := s.pipelineAccount(ctx)
	if err != nil {
		return "", err
	}

	err = s.pipelinePreprocessRecruitmentTags(ctx.UserContext(), req)
	if err != nil {
		return "", err
	}

	// Temporarily add this pipeline to convert randomMaterial_5 to randomMaterial_7 for MAA's drop. Should be removed when the event is ended.
	err = s.pipelineConvertLegacySuppliesForMaa(ctx.UserContext(), req)
	if err != nil {
		return "", err
	}

	// merge drops with same (dropType, itemId) pair
	drops, err := s.pipelineMergeDropsAndMapDropTypes(ctx.UserContext(), req.Drops)
	if err != nil {
		return "", err
	}

	singleReport := &types.ReportTaskSingleReport{
		FragmentStageID: req.FragmentStageID,
		Drops:           drops,
		// for now, we do not support multiple report by specifying `times`
		Times:    1,
		Metadata: req.Metadata,
	}

	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	err = s.pipelineAggregateGachaboxDrops(ctx.UserContext(), singleReport)
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
	// if account is not found, create new account
	accountId, err := s.pipelineAccount(ctx)
	if err != nil {
		return "", err
	}

	reports := make([]*types.ReportTaskSingleReport, len(req.BatchDrops))

	for i, drop := range req.BatchDrops {
		// merge drops with same (dropType, itemId) pair
		drops, err := s.pipelineMergeDropsAndMapDropTypes(ctx.UserContext(), drop.Drops)
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

		err = s.pipelineAggregateGachaboxDrops(ctx.UserContext(), report)
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

func (s *Report) RecallSingularReport(ctx context.Context, req *types.SingleReportRecallRequest) error {
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
