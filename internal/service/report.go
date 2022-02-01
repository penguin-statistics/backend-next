package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"

	"github.com/penguin-statistics/backend-next/internal/models/convertion"
	"github.com/penguin-statistics/backend-next/internal/models/konst"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
)

type ReportService struct {
	NatsConn       *nats.Conn
	StageRepo      *repos.StageRepo
	DropInfoRepo   *repos.DropInfoRepo
	AccountRepo    *repos.AccountRepo
	ReportVerifier *reportutils.ReportVerifier
}

func NewReportService(natsConn *nats.Conn, stageRepo *repos.StageRepo, dropInfoRepo *repos.DropInfoRepo, accountRepo *repos.AccountRepo, reportVerifier *reportutils.ReportVerifier) *ReportService {
	service := &ReportService{
		NatsConn:       natsConn,
		StageRepo:      stageRepo,
		DropInfoRepo:   dropInfoRepo,
		AccountRepo:    accountRepo,
		ReportVerifier: reportVerifier,
	}

	go func() {
		ch := make(chan error)

		go func() {
			for {
				err := <-ch
				spew.Dump(err)
			}
		}()

		service.ReportConsumeWorker(context.Background(), ch)
	}()
	return service
}

func (s *ReportService) PreprocessAndQueueSingularReport(ctx *fiber.Ctx, report *types.SingleReportRequest) error {
	// get PenguinID from HTTP header in form of Authorization: PenguinID ########
	penguinID := strings.TrimSpace(strings.TrimPrefix(ctx.Get("Authorization"), "PenguinID"))
	idempotencyKey := ctx.Get("Idempotency-Key")

	// if PenguinID is empty, create new PenguinID
	account, err := s.AccountRepo.GetAccountByPenguinId(ctx.Context(), penguinID)
	if err != nil {
		return err
	}
	var accountId int
	if account == nil {
		createdAccount, err := s.AccountRepo.CreateAccountWithRandomPenguinID(ctx.Context())
		if err != nil {
			return err
		}
		accountId = createdAccount.AccountID
	} else {
		accountId = account.AccountID
	}

	// merge drops with same (dropType, itemId) pair
	report.Drops = reportutils.MergeDrops(report.Drops)

	singleReport := convertion.SingleReportRequestToSingleReport(report)

	// for gachabox drop, we need to aggregate `times` according to `quantity` for report.Drops
	category, err := s.StageRepo.GetStageExtraProcessTypeByArkId(ctx.Context(), singleReport.StageID)
	if err != nil {
		return err
	}
	if category == konst.ExtraProcessTypeGachaBox {
		reportutils.AggregateGachaBoxDrops(singleReport)
	}

	// construct ReportContext
	reportCtx := &types.ReportContext{
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  report.Server,
			Source:  report.Source,
			Version: report.Version,
		},
		Reports:   []*types.SingleReport{singleReport},
		AccountID: accountId,
		IP:        ctx.IP(),
	}

	js, err := s.NatsConn.JetStream(nats.PublishAsyncMaxPending(128))
	if err != nil {
		return err
	}

	reportCtxJson, err := json.Marshal(reportCtx)
	if err != nil {
		return err
	}

	pub, err := js.PublishAsync("REPORT.SINGLE", reportCtxJson, nats.MsgId(idempotencyKey))
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
	case <-time.After(time.Millisecond * 500):
		return fmt.Errorf("timeout waiting for NATS response")
	}

	// if err := s.ReportVerifier.Verify(ctx.Context(), reportCtx); err != nil {
	// 	return err
	// }

	// return ctx.SendStatus(fiber.StatusAccepted)
}

func (s *ReportService) ReportConsumeWorker(ctx context.Context, ch chan error) error {
	defer close(ch)

	js, err := s.NatsConn.JetStream()
	if err != nil {
		return err
	}

	msgChan := make(chan *nats.Msg, 16)

	_, err = js.ChanQueueSubscribe("REPORT.*", "penguin-reports", msgChan, nats.AckWait(time.Second*10), nats.MaxAckPending(128))
	if err != nil {
		return err
	}

	for {
		select {
		case msg := <-msgChan:
			taskCtx, cancelTask := context.WithDeadline(ctx, time.Now().Add(time.Second*10))
			inprogressInformer := time.AfterFunc(time.Second*5, func() {
				msg.InProgress()
			})
			defer func() {
				inprogressInformer.Stop()
				cancelTask()
			}()

			reportCtx := &types.ReportContext{}
			if err := json.Unmarshal(msg.Data, reportCtx); err != nil {
				ch <- err
				msg.Nak()
				continue
			}

			err = s.consumeReportTask(taskCtx, reportCtx)
			if err != nil {
				ch <- err
				msg.Nak()
				continue
			}

			msg.Ack()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *ReportService) consumeReportTask(ctx context.Context, reportCtx *types.ReportContext) error {
	fmt.Println("now processing reportCtx: ", reportCtx)
	if err := s.ReportVerifier.Verify(ctx, reportCtx); err != nil {
		return err
	}

	fmt.Println("reportCtx verified successfully")

	return nil

	// save to database
	// if err := s.DropInfoRepo.SaveDrops(msg.Context(), reportCtx.Reports); err != nil {
	// 	continue
	// }
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
// 	reportCtx := &types.ReportContext{
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
