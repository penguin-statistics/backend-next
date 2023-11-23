package reportwkr

import (
	"context"
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
			log.Error().Err(err).Msg("failed to ack")
		}
	}()

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
		return err
	}

	start := time.Now()
	defer observability.ReportConsumeDuration.
		WithLabelValues().
		Observe(time.Since(start).Seconds())

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

	tx, err := w.DB.BeginTx(pstCtx, nil)
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

		dropPattern, created, err := w.DropPatternRepo.GetOrCreateDropPatternFromDrops(pstCtx, tx, report.Drops)
		if err != nil {
			return errors.Wrap(err, "failed to calculate drop pattern hash")
		}
		if created {
			_, err := w.DropPatternElementRepo.CreateDropPatternElements(pstCtx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return errors.Wrap(err, "failed to create drop pattern elements")
			}
		}

		stage, err := w.StageService.GetStageByArkId(pstCtx, report.StageID)
		if err != nil {
			return errors.Wrap(err, "failed to get stage")
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
		if err = w.DropReportRepo.CreateDropReport(pstCtx, tx, dropReport); err != nil {
			return errors.Wrap(err, "failed to create drop report")
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
		if err = w.DropReportExtraRepo.CreateDropReportExtra(pstCtx, tx, &model.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       reportTask.IP,
			Metadata: report.Metadata,
			MD5:      null.NewString(md5, md5 != ""),
		}); err != nil {
			return errors.Wrap(err, "failed to create drop report extra")
		}

		if err := w.Redis.Set(pstCtx, constant.ReportRedisPrefix+reportTask.TaskID, dropReport.ReportID, time.Hour*24).Err(); err != nil {
			return errors.Wrap(err, "failed to set report id in redis")
		}
	}

	intendedCommit = true
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit transaction")
	}

	return nil
}
