package reportwkr

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/observability"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/reportutil"
)

type WorkerDeps struct {
	fx.In
	ReportServices *service.Report
}

type Worker struct {
	// count is the number of workers
	count int

	WorkerDeps
}

func Start(conf *config.Config, deps WorkerDeps) {
	ch := make(chan error)
	// handle & dump errors from workers
	go func() {
		for {
			err := <-ch
			if err != nil {
				log.Error().Err(err).Msg("report worker error")
			}
		}
	}()
	// works like a consumer factory
	reportWorkers := &Worker{
		count:      0,
		WorkerDeps: deps,
	}
	// spawn workers
	// maybe we should specify the number of worker in config.Config ?
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			err := reportWorkers.Consumer(context.Background(), ch)
			if err != nil {
				ch <- err
			}
		}()
		// update current worker count
		reportWorkers.count += 1
	}
}

func (w *Worker) Consumer(ctx context.Context, ch chan error) error {
	msgChan := make(chan *nats.Msg, 16)

	_, err := w.ReportServices.NatsJS.ChanQueueSubscribe("REPORT.*", "penguin-reports", msgChan, nats.AckWait(time.Second*10), nats.MaxAckPending(128))
	if err != nil {
		log.Err(err).Msg("failed to subscribe to REPORT.*")
		return err
	}

	for {
		select {
		case msg := <-msgChan:
			func() {
				taskCtx, cancelTask := context.WithTimeout(ctx, time.Second*10)
				inprogressInformer := time.AfterFunc(time.Second*5, func() {
					err = msg.InProgress()
					if err != nil {
						log.Error().Err(err).Msg("failed to set msg InProgress")
					}
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

				start := time.Now()
				defer func() {
					observability.ReportConsumeDuration.
						WithLabelValues().
						Observe(time.Since(start).Seconds())
				}()

				err = w.consumeReport(taskCtx, reportTask)
				if err != nil {
					log.Error().
						Err(err).
						Str("taskId", reportTask.TaskID).
						Interface("reportTask", reportTask).
						Msg("failed to consume report task")
					ch <- err
					return
				}

				log.Info().
					Str("taskId", reportTask.TaskID).
					Dur("duration", time.Since(start)).
					Msg("report task processed successfully")
			}()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *Worker) consumeReport(ctx context.Context, reportTask *types.ReportTask) error {
	L := log.With().
		Interface("task", reportTask).
		Logger()

	L.Info().Msg("now processing new report task")

	violations := w.ReportServices.ReportVerifier.Verify(ctx, reportTask)
	if len(violations) > 0 {
		L.Warn().
			Interface("violations", violations).
			Msg("report task verification failed on some or all reports")
	}

	// reportTask.CreatedAt is in microseconds
	var taskCreatedAt time.Time
	if reportTask.CreatedAt != 0 {
		taskCreatedAt = time.UnixMicro(reportTask.CreatedAt)
	} else {
		taskCreatedAt = time.Now()
	}

	tx, err := w.ReportServices.DB.BeginTx(ctx, nil)
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
	for idx, report := range reportTask.Reports {
		report.Drops = reportutil.MergeDropsByItemID(report.Drops)

		dropPattern, created, err := w.ReportServices.DropPatternRepo.GetOrCreateDropPatternFromDrops(ctx, tx, report.Drops)
		if err != nil {
			return errors.Wrap(err, "failed to calculate drop pattern hash")
		}
		if created {
			_, err := w.ReportServices.DropPatternElementRepo.CreateDropPatternElements(ctx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return errors.Wrap(err, "failed to create drop pattern elements")
			}
		}

		stage, err := w.ReportServices.StageRepo.GetStageByArkId(ctx, report.StageID)
		if err != nil {
			return errors.Wrap(err, "failed to get stage")
		}

		dropReport := &model.DropReport{
			StageID:     stage.StageID,
			PatternID:   dropPattern.PatternID,
			Times:       report.Times,
			CreatedAt:   &taskCreatedAt,
			Reliability: violations.Reliability(idx),
			Server:      reportTask.Server,
			AccountID:   reportTask.AccountID,
		}
		if err = w.ReportServices.DropReportRepo.CreateDropReport(ctx, tx, dropReport); err != nil {
			return errors.Wrap(err, "failed to create drop report")
		}

		md5 := ""
		if report.Metadata != nil && report.Metadata.MD5 != "" {
			md5 = report.Metadata.MD5
		}
		if reportTask.IP == "" {
			// FIXME: temporary hack; find why ip is empty
			reportTask.IP = "127.0.0.1"
		}
		if err = w.ReportServices.DropReportExtraRepo.CreateDropReportExtra(ctx, tx, &model.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       reportTask.IP,
			Source:   reportTask.Source,
			Version:  reportTask.Version,
			Metadata: report.Metadata,
			MD5:      null.NewString(md5, md5 != ""),
		}); err != nil {
			return errors.Wrap(err, "failed to create drop report extra")
		}

		if err := w.ReportServices.Redis.Set(ctx, reportTask.TaskID, dropReport.ReportID, time.Hour*24).Err(); err != nil {
			return errors.Wrap(err, "failed to set report id in redis")
		}
	}

	intendedCommit = true
	return tx.Commit()
}
