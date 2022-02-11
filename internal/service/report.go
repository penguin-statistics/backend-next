package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/konst"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
)

type ReportService struct {
	DB                     *bun.DB
	NatsJS                 nats.JetStreamContext
	Redis                  *redis.Client
	ItemRepo               *repos.ItemRepo
	StageService           *StageService
	AccountService         *AccountService
	DropInfoRepo           *repos.DropInfoRepo
	DropReportRepo         *repos.DropReportRepo
	DropPatternRepo        *repos.DropPatternRepo
	DropReportExtraRepo    *repos.DropReportExtraRepo
	DropPatternElementRepo *repos.DropPatternElementRepo
	ReportVerifier         *reportutils.ReportVerifier
}

func NewReportService(db *bun.DB, natsJs nats.JetStreamContext, redis *redis.Client, itemRepo *repos.ItemRepo, stageService *StageService, dropInfoRepo *repos.DropInfoRepo, dropReportRepo *repos.DropReportRepo, dropReportExtraRepo *repos.DropReportExtraRepo, dropPatternRepo *repos.DropPatternRepo, dropPatternElementRepo *repos.DropPatternElementRepo, accountService *AccountService, reportVerifier *reportutils.ReportVerifier) *ReportService {
	service := &ReportService{
		DB:                     db,
		NatsJS:                 natsJs,
		Redis:                  redis,
		ItemRepo:               itemRepo,
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

		for i := 0; i < 5; i++ {
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

func (s *ReportService) PreprocessAndQueueSingularReport(ctx *fiber.Ctx, req *types.SingleReportRequest) error {
	// if account is not found, create new account
	var accountId int
	account, err := s.AccountService.GetAccountFromRequest(ctx)
	if err != nil {
		createdAccount, err := s.AccountService.CreateAccountWithRandomPenguinID(ctx)
		if err != nil {
			return err
		}
		accountId = createdAccount.AccountID
		utils.SetPenguinIDToResponse(ctx, createdAccount.PenguinID)
	} else {
		accountId = account.AccountID
	}

	// idempotencyKey := ctx.Get("Idempotency-Key", ctx.Locals("requestid").(string))

	// merge drops with same (dropType, itemId) pair
	req.Drops = reportutils.MergeDrops(req.Drops)

	drops := make([]*types.Drop, 0, len(req.Drops))
	for _, drop := range req.Drops {
		item, err := s.ItemRepo.GetItemByArkId(ctx.Context(), drop.ItemID)
		if err != nil {
			return err
		}

		drops = append(drops, &types.Drop{
			DropType: drop.DropType,
			ItemID:   item.ItemID,
			Quantity: drop.Quantity,
		})
	}

	singleReport := &types.SingleReport{
		FragmentStageID: req.FragmentStageID,
		Drops:           drops,
	}

	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	category, err := s.StageService.GetStageExtraProcessTypeByArkId(ctx, singleReport.StageID)
	if err != nil {
		return err
	}
	if category == konst.ExtraProcessTypeGachaBox {
		reportutils.AggregateGachaBoxDrops(singleReport)
	}

	taskId := fmt.Sprintf("%s-%d", ctx.Locals("requestid").(string), accountId)

	// construct ReportContext
	reportTask := &types.ReportTask{
		TaskID: taskId,
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  req.Server,
			Source:  req.Source,
			Version: req.Version,
		},
		Reports:   []*types.SingleReport{singleReport},
		AccountID: accountId,
		IP:        utils.ExtractIP(ctx),
	}

	reportTaskJson, err := json.Marshal(reportTask)
	if err != nil {
		return err
	}

	pub, err := s.NatsJS.PublishAsync("REPORT.SINGLE", reportTaskJson)
	if err != nil {
		return err
	}

	select {
	case err := <-pub.Err():
		return err
	case <-pub.Ok():
		return nil
	case <-ctx.Context().Done():
		return ctx.Context().Err()
	case <-time.After(time.Second * 2):
		return fmt.Errorf("timeout waiting for NATS response")
	}

	// if err := s.ReportVerifier.Verify(ctx.Context(), reportTask); err != nil {
	// 	return err
	// }

	// return ctx.SendStatus(fiber.StatusAccepted)
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
					msg.Ack()
				}()

				reportTask := &types.ReportTask{}
				if err := json.Unmarshal(msg.Data, reportTask); err != nil {
					ch <- err
					return
				}

				err = s.consumeReportTask(taskCtx, reportTask)
				if err != nil {
					ch <- err
					return
				}
			}()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *ReportService) consumeReportTask(ctx context.Context, reportTask *types.ReportTask) error {
	log.Debug().Msg("now processing new report task")
	taskReliability := 0
	if err := s.ReportVerifier.Verify(ctx, reportTask); err != nil {
		// TODO: use different error code for different types of error
		taskReliability = 1
		log.Warn().Err(err).Msg("report task verification failed, marking task as unreliable")
	}
	log.Debug().Msg("report task verification finished")

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	intendedCommit := false
	defer func() {
		if !intendedCommit {
			tx.Rollback()
		}
	}()

	// calculate drop pattern hash for each report
	for _, report := range reportTask.Reports {
		dropPatternHash := reportutils.CalculateDropPatternHash(report.Drops)
		dropPattern, created, err := s.DropPatternRepo.GetOrCreateDropPatternByHash(ctx, tx, dropPatternHash)
		if err != nil {
			return err
		}
		if created {
			_, err := s.DropPatternElementRepo.CreateDropPatternElements(ctx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return err
			}
		}

		// FIXME: the param is context.Context, so we have to use repo here, can we change it to use *fiber.Ctx?
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
			// panic(err)
			return err
		}

		var md5 null.String
		if report.Metadata != nil {
			md5 = report.Metadata.MD5
		}
		ip := reportTask.IP
		if ip == "" {
			// FIXME: temporary hack; find why ip is empty
			ip = "127.0.0.1"
		}
		if err = s.DropReportExtraRepo.CreateDropReportExtra(ctx, tx, &models.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       ip,
			Source:   reportTask.Source,
			Version:  reportTask.Version,
			Metadata: report.Metadata,
			MD5:      md5,
		}); err != nil {
			return err
		}

		s.Redis.Set(ctx, fmt.Sprintf("report:%s", reportTask.TaskID), fmt.Sprintf("%d", dropReport.ReportID), time.Hour*24)
	}

	intendedCommit = true
	return tx.Commit()
}

// func (s *ReportService) VerifyAndSubmitBatchReport(ctx *fiber.Ctx, report *types.BatchReportRequest) error {
// 	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
// 	penguinID := strings.TrimSpace(strings.TrimPrefix(ctx.Get("Authorization"), "PenguinID"))

// 	// if PenguinID is empty, create new PenguinID
// 	account, err := s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinID)
// 	if err != nil {
// 		return err
// 	}
// 	var accountId int
// 	if account == nil {
// 		createdAccount, err := s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
// 		if err != nil {
// 			return err
// 		}
// 		accountId = createdAccount.AccountID
// 	} else {
// 		accountId = account.AccountID
// 	}

// 	// merge drops with same (dropType, itemId) pair
// 	for _, report := range report.Reports {
// 		report.Drops = reportutils.MergeDrops(report.Drops)
// 	}

// 	batchReport := convertion.BatchReportRequestToBatchReport(report)

// 	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
// 	for _, report := range batchReport.Reports {
// 		category, err := s.StageRepo.GetStageExtraProcessTypeByArkId(ctx.Context(), report.StageID)
// 		if err != nil {
// 			return err
// 		}
// 		if category == konst.ExtraProcessTypeGachaBox {
// 			reportutils.AggregateGachaBoxDrops(report)
// 		}
// 	}

// 	// construct ReportContext
// 	reportTask := &types.ReportContext{
// 		FragmentReportCommon: types.FragmentReportCommon{
// 			Server:  report.Server,
// 			Source:  report.Source,
// 			Version: report.Version,
// 		},
// 		Reports:  batchReport.Reports,
// 		AccountID: accountId,
// 		IP:        ctx.IP(),
// 	}
// }
