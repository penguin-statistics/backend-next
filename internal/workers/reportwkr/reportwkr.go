package reportwkr

import (
	"context"
	"database/sql"
	"runtime"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/jetstream"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportutil"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

var tracer = otel.Tracer("reportwkr")

type WorkerDeps struct {
	fx.In
	DB                     *bun.DB
	Redis                  *redis.Client
	NatsJS                 nats.JetStreamContext
	StageService           *service.Stage
	DropReportRepo         *repo.DropReport
	DropPatternRepo        *repo.DropPattern
	DropReportExtraRepo    *repo.DropReportExtra
	DropPatternElementRepo *repo.DropPatternElement
	ReportVerifier         *reportverifs.ReportVerifiers
}

type Worker struct {
	// count is the number of workers
	count int

	WorkerDeps
}

func Start(conf *appconfig.Config, deps WorkerDeps) {
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
	// maybe we should specify the number of worker in appconfig.Config ?
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
	msgChan := make(chan *nats.Msg, 512)

	_, err := w.NatsJS.ChanQueueSubscribe("REPORT.*", "penguin-reports", msgChan, nats.AckWait(time.Second*10), nats.MaxAckPending(128))
	if err != nil {
		log.Err(err).Msg("failed to subscribe to REPORT.*")
		return err
	}

	for {
		select {
		case msg := <-msgChan:
			err = w.ingestPreprocess(ctx, msg)
			if err != nil {
				log.Err(err).Msg("failed to ingest preprocess")
				ch <- err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *Worker) ingestPreprocess(ctx context.Context, msg *nats.Msg) error {
	defer func() {
		if err := msg.Ack(); err != nil {
			observability.ReportConsumeAckFailures.Inc()
			log.Error().Err(err).Msg("failed to ack")
		}
	}()

	outcome := "failed_process"
	defer func() {
		observability.ReportConsumeTotal.
			WithLabelValues(outcome, observability.ReportSubjectLabel(msg.Subject)).
			Inc()
	}()
	if metadata, err := msg.Metadata(); err == nil {
		observability.ReportConsumeNumDelivered.Observe(float64(metadata.NumDelivered))
	}

	taskCtx, cancelTask := context.WithTimeout(ctx, time.Second*10)
	defer cancelTask()

	inprogressInformer := time.AfterFunc(time.Second*5, func() {
		err := msg.InProgress()
		if err != nil {
			log.Error().Err(err).Msg("failed to set msg InProgress")
		}
	})
	defer inprogressInformer.Stop()

	reportTask := &types.ReportTask{}
	if err := json.Unmarshal(msg.Data, reportTask); err != nil {
		outcome = "failed_unmarshal"
		return err
	}

	start := time.Now()
	defer func() {
		observability.ReportConsumeDuration.
			WithLabelValues().
			Observe(time.Since(start).Seconds())
	}()

	metadata, err := msg.Metadata()
	if err != nil {
		// should not happen: the message should be always a jetstream message
		return err
	}

	var span trace.Span
	taskCtx, span = tracer.
		Start(taskCtx, "reportwkr.ConsumeTask",
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				semconv.MessagingSystemKey.String("nats"),
				semconv.MessagingDestinationNameKey.String(msg.Subject),
				semconv.MessagingMessageIDKey.String(jetstream.MessageID(metadata.Sequence)),
				semconv.MessagingMessagePayloadSizeBytesKey.Int(len(msg.Data)),
			))

	err = w.process(taskCtx, reportTask)
	if err != nil {
		log.Error().
			Err(err).
			Str("taskId", reportTask.TaskID).
			Interface("reportTask", reportTask).
			Msg("failed to consume report task")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return err
	}
	span.SetStatus(codes.Ok, "")
	span.End()

	log.Info().
		Str("evt.name", "reportwkr.processed").
		Str("taskId", reportTask.TaskID).
		Interface("task", reportTask).
		Dur("duration", time.Since(start)).
		Msg("report task processed successfully")

	outcome = "stored"
	return nil
}

func (w *Worker) process(ctx context.Context, reportTask *types.ReportTask) error {
	L := log.With().
		Interface("task", reportTask).
		Logger()

	L.Info().
		Str("evt.name", "reportwkr.received").
		Msg("received report task: processing")

	// reportTask.CreatedAt is in microseconds
	taskCreatedAt := time.UnixMicro(reportTask.CreatedAt)

	observability.ReportConsumeMessagingLatency.
		WithLabelValues().
		Observe(time.Since(taskCreatedAt).Seconds())

	verifyCtx, verifySpan := tracer.
		Start(ctx, "reportwkr.process.Verify",
			trace.WithSpanKind(trace.SpanKindInternal))

	violations := w.ReportVerifier.Verify(verifyCtx, reportTask)
	if len(violations) > 0 {
		L.Warn().
			Str("evt.name", "reportwkr.violations").
			Stringer("violations", violations).
			Msg("report task verification failed on some or all reports")
	}

	verifySpan.End()

	pstCtx, pstSpan := tracer.
		Start(ctx, "reportwkr.process.Persistence",
			trace.WithSpanKind(trace.SpanKindInternal))
	defer pstSpan.End()

	// the transaction is rolled back when persist returns an error, and committed otherwise
	var reportId int
	err := w.DB.RunInTx(pstCtx, nil, func(ctx context.Context, tx bun.Tx) (err error) {
		reportId, err = w.persist(ctx, tx, reportTask, taskCreatedAt, violations)
		if err != nil {
			// rolled back here rather than by RunInTx, which drops the error of a failed rollback. A failed commit
			// is not rolled back: it has already ended the transaction. Neither has a transaction database/sql
			// rolled back as its context ended (the task's deadline), which is not a failed rollback.
			L.Warn().Msg("rolling back transaction due to error")
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				L.Error().Err(err).Msg("failed to rollback transaction")
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	observability.ReportStoredTotal.
		WithLabelValues(observability.ReportSourceLabel(reportTask.Source)).
		Add(float64(len(reportTask.Reports)))

	// The recall key holds the last report of the task (only singular reports are recalled), set once it is
	// committed so that a recall cannot come before the report exists. Best-effort: the task is stored, so this must
	// not fail it.
	if reportId != 0 {
		if err := w.Redis.Set(pstCtx, constant.ReportRedisPrefix+reportTask.TaskID, reportId, time.Hour*24).Err(); err != nil {
			log.Warn().
				Err(err).
				Str("evt.name", "reportwkr.recall.unset").
				Str("taskId", reportTask.TaskID).
				Msg("failed to set report id in redis; the report cannot be recalled")
		}
	}

	return nil
}

// persist stores the reports of the task in tx, and returns the id of the last report of the task (0 if it has
// none).
func (w *Worker) persist(ctx context.Context, tx bun.Tx, reportTask *types.ReportTask, taskCreatedAt time.Time, violations reportverifs.Violations) (int, error) {
	var reportId int
	// calculate drop pattern hash for each report
	for idx, report := range reportTask.Reports {
		report.Drops = reportutil.MergeDropsByItemID(report.Drops)

		dropPattern, created, err := w.DropPatternRepo.GetOrCreateDropPatternFromDrops(ctx, tx, report.Drops)
		if err != nil {
			return 0, errors.Wrap(err, "failed to calculate drop pattern hash")
		}
		if created {
			_, err := w.DropPatternElementRepo.CreateDropPatternElements(ctx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return 0, errors.Wrap(err, "failed to create drop pattern elements")
			}
		}

		stage, err := w.StageService.GetStageByArkId(ctx, report.StageID)
		if err != nil {
			return 0, errors.Wrap(err, "failed to get stage")
		}

		reliability := violations.Reliability(idx)

		dropReport := &model.DropReport{
			StageID:     stage.StageID,
			PatternID:   dropPattern.PatternID,
			Times:       report.Times,
			CreatedAt:   &taskCreatedAt,
			Reliability: reliability,
			Server:      reportTask.Server,
			AccountID:   reportTask.AccountID,
			SourceName:  reportTask.Source,
			Version:     reportTask.Version,
		}
		if err = w.DropReportRepo.CreateDropReport(ctx, tx, dropReport); err != nil {
			return 0, errors.Wrap(err, "failed to create drop report")
		}

		observability.ReportReliability.WithLabelValues(strconv.Itoa(reliability), reportTask.Source).Inc()

		md5 := ""
		if report.Metadata != nil && report.Metadata.MD5 != "" {
			md5 = report.Metadata.MD5
		}
		if reportTask.IP == "" {
			// FIXME: temporary hack; find why ip is empty
			log.Warn().
				Str("evt.name", "reportwkr.ip.empty").
				Str("taskId", reportTask.TaskID).
				Interface("reportTask", reportTask).
				Msg("ip is empty; using 127.0.0.1 as a fallback")

			reportTask.IP = "127.0.0.1"
		}
		if err = w.DropReportExtraRepo.CreateDropReportExtra(ctx, tx, &model.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       reportTask.IP,
			Metadata: report.Metadata,
			MD5:      null.NewString(md5, md5 != ""),
		}); err != nil {
			return 0, errors.Wrap(err, "failed to create drop report extra")
		}

		reportId = dropReport.ReportID
	}

	return reportId, nil
}
