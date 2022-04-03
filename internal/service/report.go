package service

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/dchest/uniuri"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
)

var (
	ErrReportNotFound = pgerr.ErrInvalidReq.Msg("report not existed or has already been recalled")
	ErrNatsTimeout    = errors.New("timeout waiting for NATS response")
)

type ReportService struct {
	DB                     *bun.DB
	Redis                  *redis.Client
	NatsJS                 nats.JetStreamContext
	ItemService            *ItemService
	StageService           *StageService
	AccountService         *AccountService
	DropInfoRepo           *repo.DropInfo
	DropReportRepo         *repo.DropReport
	DropPatternRepo        *repo.DropPattern
	DropReportExtraRepo    *repo.DropReportExtra
	DropPatternElementRepo *repo.DropPatternElement
	ReportVerifier         *reportutils.ReportVerifier
}

func NewReportService(db *bun.DB, redisClient *redis.Client, natsJs nats.JetStreamContext, itemService *ItemService, stageService *StageService, dropInfoRepo *repo.DropInfo, dropReportRepo *repo.DropReport, dropReportExtraRepo *repo.DropReportExtra, dropPatternRepo *repo.DropPattern, dropPatternElementRepo *repo.DropPatternElement, accountService *AccountService, reportVerifier *reportutils.ReportVerifier) *ReportService {
	service := &ReportService{
		DB:                     db,
		Redis:                  redisClient,
		NatsJS:                 natsJs,
		ItemService:            itemService,
		StageService:           stageService,
		AccountService:         accountService,
		DropInfoRepo:           dropInfoRepo,
		DropReportRepo:         dropReportRepo,
		DropPatternRepo:        dropPatternRepo,
		DropReportExtraRepo:    dropReportExtraRepo,
		DropPatternElementRepo: dropPatternElementRepo,
		ReportVerifier:         reportVerifier,
	}

	// TODO: isolate report consumer as standalone workers
	go func() {
		ch := make(chan error)

		go func() {
			for {
				err := <-ch
				spew.Dump(err)
			}
		}()

		for i := 0; i < runtime.NumCPU(); i++ {
			go func() {
				err := service.ReportConsumeWorker(context.Background(), ch)
				if err != nil {
					ch <- err
				}
			}()
		}
	}()
	return service
}

func (s *ReportService) pipelineAccount(ctx *fiber.Ctx) (accountId int, err error) {
	account, err := s.AccountService.GetAccountFromRequest(ctx)
	if err != nil {
		createdAccount, err := s.AccountService.CreateAccountWithRandomPenguinId(ctx.Context())
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

func (s *ReportService) pipelineMergeDropsAndMapDropTypes(ctx context.Context, drops []types.ArkDrop) ([]*types.Drop, error) {
	drops = reportutils.MergeDrops(drops)

	convertedDrops := make([]*types.Drop, 0, len(drops))
	for _, drop := range drops {
		item, err := s.ItemService.GetItemByArkId(ctx, drop.ItemID)
		if err != nil {
			return nil, err
		}

		convertedDrops = append(convertedDrops, &types.Drop{
			// maps DropType to DB DropType
			DropType: constants.DropTypeMap[drop.DropType],
			ItemID:   item.ItemID,
			Quantity: drop.Quantity,
		})
	}

	return convertedDrops, nil
}

func (s *ReportService) pipelineTaskId(ctx *fiber.Ctx) string {
	return ctx.Locals("requestid").(string) + "-" + uniuri.NewLen(32)
}

func (s *ReportService) commitReportTask(ctx *fiber.Ctx, subject string, task *types.ReportTask) (taskId string, err error) {
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
	case <-ctx.Context().Done():
		return "", ctx.Context().Err()
	case <-time.After(time.Second * 10):
		return "", ErrNatsTimeout
	}
}

// returns taskID and error, if any
func (s *ReportService) PreprocessAndQueueSingularReport(ctx *fiber.Ctx, req *types.SingleReportRequest) (taskId string, err error) {
	// if account is not found, create new account
	accountId, err := s.pipelineAccount(ctx)
	if err != nil {
		return "", err
	}

	// merge drops with same (dropType, itemId) pair
	drops, err := s.pipelineMergeDropsAndMapDropTypes(ctx.Context(), req.Drops)
	if err != nil {
		return "", err
	}

	singleReport := &types.SingleReport{
		FragmentStageID: req.FragmentStageID,
		Drops:           drops,
	}

	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	category, err := s.StageService.GetStageExtraProcessTypeByArkId(ctx.Context(), singleReport.StageID)
	if err != nil {
		return "", err
	}
	if category.Valid && category.String == constants.ExtraProcessTypeGachaBox {
		reportutils.AggregateGachaBoxDrops(singleReport)
	}

	// construct ReportContext
	reportTask := &types.ReportTask{
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  req.Server,
			Source:  req.Source,
			Version: req.Version,
		},
		Reports:   []*types.SingleReport{singleReport},
		AccountID: accountId,
		IP:        utils.ExtractIP(ctx),
	}

	return s.commitReportTask(ctx, "REPORT.SINGLE", reportTask)
}

func (s *ReportService) PreprocessAndQueueBatchReport(ctx *fiber.Ctx, req *types.BatchReportRequest) (taskId string, err error) {
	// if account is not found, create new account
	accountId, err := s.pipelineAccount(ctx)
	if err != nil {
		return "", err
	}

	reports := make([]*types.SingleReport, len(req.BatchDrops))

	for i, drop := range req.BatchDrops {
		// merge drops with same (dropType, itemId) pair
		drops, err := s.pipelineMergeDropsAndMapDropTypes(ctx.Context(), drop.Drops)
		if err != nil {
			return "", err
		}

		reports[i] = &types.SingleReport{
			FragmentStageID: drop.FragmentStageID,
			Drops:           drops,
			Metadata:        &drop.Metadata,
		}
	}

	// construct ReportContext
	reportTask := &types.ReportTask{
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  req.Server,
			Source:  req.Source,
			Version: req.Version,
		},
		Reports:   reports,
		AccountID: accountId,
		IP:        utils.ExtractIP(ctx),
	}

	return s.commitReportTask(ctx, "REPORT.BATCH", reportTask)
}

func (s *ReportService) RecallSingularReport(ctx context.Context, req *types.SingleReportRecallRequest) error {
	var reportId int
	r := s.Redis.Get(ctx, req.ReportHash)

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

func (s *ReportService) ReportConsumeWorker(ctx context.Context, ch chan error) error {
	msgChan := make(chan *nats.Msg, 16)

	_, err := s.NatsJS.ChanQueueSubscribe("REPORT.*", "penguin-reports", msgChan, nats.AckWait(time.Second*10), nats.MaxAckPending(128))
	if err != nil {
		log.Err(err).Msg("failed to subscribe to REPORT.*")
		return err
	}

	time.Now().UnixMilli()

	for {
		select {
		case msg := <-msgChan:
			func() {
				taskCtx, cancelTask := context.WithDeadline(ctx, time.Now().Add(time.Second*10))
				inprogressInformer := time.AfterFunc(time.Second*5, func() {
					msg.InProgress()
				})
				defer func() {
					inprogressInformer.Stop()
					cancelTask()
					if err := msg.Ack(); err != nil {
						log.Error().Err(err).Msg("failed to ack")
					}
				}()

				reportTask := &types.ReportTask{}
				if err := json.Unmarshal(msg.Data, reportTask); err != nil {
					ch <- err
					return
				}

				err = s.consumeReportTask(taskCtx, reportTask)
				if err != nil {
					log.Error().
						Err(err).
						Str("taskId", reportTask.TaskID).
						Str("reportTask", spew.Sdump(reportTask)).
						Msg("failed to consume report task")
					ch <- err
					return
				}

				log.Info().Str("taskId", reportTask.TaskID).Msg("report task processed successfully")
			}()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *ReportService) consumeReportTask(ctx context.Context, reportTask *types.ReportTask) error {
	L := log.With().
		Interface("task", reportTask).
		Logger()

	L.Info().Msg("now processing new report task")
	taskReliability := 0
	if errs := s.ReportVerifier.Verify(ctx, reportTask); len(errs) > 0 {
		// TODO: use different error code for different types of error
		taskReliability = 1
		L.Warn().
			Interface("errors", errs).
			Msg("report task verification failed, marking task as unreliable")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	intendedCommit := false
	defer func() {
		if !intendedCommit {
			L.Warn().Msg("rolling back transaction due to error")
			if err := tx.Rollback(); err != nil {
				L.Error().Err(err).Msg("failed to rollback transaction")
			}
		}
	}()

	// calculate drop pattern hash for each report
	for _, report := range reportTask.Reports {
		dropPattern, created, err := s.DropPatternRepo.GetOrCreateDropPatternFromDrops(ctx, tx, report.Drops)
		if err != nil {
			return err
		}
		if created {
			_, err := s.DropPatternElementRepo.CreateDropPatternElements(ctx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return err
			}
		}

		// FIXME: the param is context.Context, so we have to use repo here, can we change it to use context.Context?
		// unable: consumer workers are not able to use context.Context as ops here are not initiated due to a fiber request,
		// but rather a message dispatch
		stage, err := s.StageService.StageRepo.GetStageByArkId(ctx, report.StageID)
		if err != nil {
			return err
		}
		times := report.Times
		if times == 0 {
			times = 1
		}
		dropReport := &models.DropReport{
			StageID:     stage.StageID,
			PatternID:   dropPattern.PatternID,
			Times:       times,
			Reliability: taskReliability,
			Server:      reportTask.Server,
			AccountID:   reportTask.AccountID,
		}
		if err = s.DropReportRepo.CreateDropReport(ctx, tx, dropReport); err != nil {
			return err
		}

		var md5 null.String
		if report.Metadata != nil {
			md5 = report.Metadata.MD5
		}
		if reportTask.IP == "" {
			// FIXME: temporary hack; find why ip is empty
			reportTask.IP = "127.0.0.1"
		}
		if err = s.DropReportExtraRepo.CreateDropReportExtra(ctx, tx, &models.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       reportTask.IP,
			Source:   reportTask.Source,
			Version:  reportTask.Version,
			Metadata: report.Metadata,
			MD5:      md5,
		}); err != nil {
			return err
		}

		if err := s.Redis.Set(ctx, reportTask.TaskID, dropReport.ReportID, time.Hour*24).Err(); err != nil {
			return err
		}
	}

	intendedCommit = true
	return tx.Commit()
}
