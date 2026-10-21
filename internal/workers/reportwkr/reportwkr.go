package reportwkr

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
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
	NatsConn               *nats.Conn
	StageService           *service.Stage
	DropReportRepo         *repo.DropReport
	DropPatternRepo        *repo.DropPattern
	DropReportExtraRepo    *repo.DropReportExtra
	DropPatternElementRepo *repo.DropPatternElement
	ReportVerifier         *reportverifs.ReportVerifiers
}

// consumerConfig is the configuration of the report consumer. Tests run it on streams of their own, with the times
// scaled down.
type consumerConfig struct {
	stream  string
	durable string
	// legacyDurable is the push consumer of the previous versions, deleted on startup
	legacyDurable string
	subject       string
	workers       int
	// ackWait is how long the server waits for the outcome of a delivered task before it delivers the task again. It
	// is far longer than taskTimeout, so that a task is delivered again only when its worker is gone.
	ackWait       time.Duration
	maxAckPending int
	taskTimeout   time.Duration
	// fetchWait is how long a worker waits for a task before checking whether it is stopped
	fetchWait time.Duration
}

var defaultConsumerConfig = consumerConfig{
	stream:        "penguin-reports",
	durable:       "penguin-reports-pull",
	legacyDurable: "penguin-reports",
	subject:       "REPORT.*",
	workers:       4, // the cores of the production node, as runtime.NumCPU() was
	ackWait:       time.Minute,
	maxAckPending: 64,
	taskTimeout:   time.Second * 10,
	fetchWait:     time.Second * 2,
}

type Worker struct {
	WorkerDeps

	conf consumerConfig
	// handle processes a task: process, or a stand-in in tests
	handle func(ctx context.Context, reportTask *types.ReportTask) error

	// subs holds a subscription to the durable per worker
	subs     []*nats.Subscription
	stopping chan struct{}
	wg       sync.WaitGroup
}

func newWorker(deps WorkerDeps, conf consumerConfig) *Worker {
	w := &Worker{
		WorkerDeps: deps,
		conf:       conf,
		stopping:   make(chan struct{}),
	}
	w.handle = w.process
	return w
}

// Start sets the report consumer up, failing the startup if it cannot, and runs its workers while the app runs.
func Start(lc fx.Lifecycle, deps WorkerDeps) error {
	w := newWorker(deps, defaultConsumerConfig)
	if err := w.subscribe(); err != nil {
		return err
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			w.start()
			return nil
		},
		OnStop: w.stop,
	})
	return nil
}

// subscribe sets the consumer up and binds a subscription of each worker to it.
func (w *Worker) subscribe() error {
	if err := w.setUpConsumer(); err != nil {
		return err
	}
	return w.bind()
}

// setUpConsumer deletes the push consumer of the previous versions, then creates the durable pull consumer of the
// report tasks, or updates the one that exists to the configuration.
func (w *Worker) setUpConsumer() error {
	c := w.conf
	// The push consumer of the previous versions, which a pod of the previous version may still consume during the
	// rollout. A work queue stream allows a single consumer per subject, so it is deleted before the pull consumer is
	// created. The pull consumer has a name of its own so that the acks of the old pod, addressed to the consumer by
	// name, go to no consumer: the tasks the old pod holds stay in the stream and are delivered again to the pull
	// consumer, so none is lost (one the old pod stored meanwhile is stored a second time). Under the same name, an
	// ack of the old pod would remove a task the pull consumer is still processing, and some server versions (2.10.17
	// to 2.10.25) would move the pull consumer past every task the old pod holds and never acks, which would then
	// never be delivered.
	_, err := w.NatsJS.ConsumerInfo(c.stream, c.legacyDurable)
	switch {
	case errors.Is(err, nats.ErrConsumerNotFound):
	case err != nil:
		return errors.Wrap(err, "failed to get legacy report consumer")
	default:
		log.Warn().
			Str("evt.name", "reportwkr.consumer.migrate").
			Str("consumer", c.legacyDurable).
			Msg("deleting the push consumer of a previous version")
		if err := w.NatsJS.DeleteConsumer(c.stream, c.legacyDurable); err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
			return errors.Wrap(err, "failed to delete legacy report consumer")
		}
	}

	info, err := w.NatsJS.ConsumerInfo(c.stream, c.durable)
	switch {
	case errors.Is(err, nats.ErrConsumerNotFound):
		return w.addConsumer()
	case err != nil:
		return errors.Wrap(err, "failed to get report consumer")
	case info.Config.AckWait != c.ackWait || info.Config.MaxAckPending != c.maxAckPending:
		cfg := info.Config
		cfg.AckWait = c.ackWait
		cfg.MaxAckPending = c.maxAckPending
		if _, err := w.NatsJS.UpdateConsumer(c.stream, &cfg); err != nil {
			return errors.Wrap(err, "failed to update report consumer")
		}
	}
	return nil
}

// bind binds a subscription of each worker to the durable, which must exist. Bind: the library did not create the
// durable, so unsubscribing or draining never deletes it, and a durable deleted meanwhile fails the startup instead of
// being created again with the defaults of the library.
func (w *Worker) bind() error {
	c := w.conf
	for i := 0; i < c.workers; i++ {
		sub, err := w.NatsJS.PullSubscribe(c.subject, c.durable, nats.Bind(c.stream, c.durable))
		if err != nil {
			return errors.Wrap(err, "failed to subscribe to report consumer")
		}
		w.subs = append(w.subs, sub)
	}
	return nil
}

func (w *Worker) addConsumer() error {
	c := w.conf
	_, err := w.NatsJS.AddConsumer(c.stream, &nats.ConsumerConfig{
		Durable:       c.durable,
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       c.ackWait,
		MaxAckPending: c.maxAckPending,
		FilterSubject: c.subject,
	})
	return errors.Wrap(err, "failed to create report consumer")
}

// start runs a worker per subscription.
func (w *Worker) start() {
	for _, sub := range w.subs {
		w.wg.Add(1)
		go w.consume(sub)
	}
}

// stop stops the workers from fetching tasks, and waits until ctx ends for the tasks in progress, which finish on
// their own context.
func (w *Worker) stop(ctx context.Context) error {
	close(w.stopping)
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// consume fetches and ingests tasks one at a time until the worker is stopped. The server delivers a task only when
// a worker asks for one, so no task waits in the client while its AckWait runs.
func (w *Worker) consume(sub *nats.Subscription) {
	defer w.wg.Done()
	for {
		select {
		case <-w.stopping:
			return
		default:
		}

		if !w.waitConnected() {
			return
		}

		msgs, err := sub.Fetch(1, nats.MaxWait(w.conf.fetchWait))
		if errors.Is(err, nats.ErrTimeout) {
			// no task queued
			continue
		}
		if err != nil {
			log.Error().Err(err).Str("evt.name", "reportwkr.fetch.failed").Msg("failed to fetch report task")
			// such as while reconnecting: wait a little before trying again
			select {
			case <-w.stopping:
				return
			case <-time.After(w.conf.fetchWait):
			}
			continue
		}

		if err := w.ingestPreprocess(context.Background(), msgs[0]); err != nil {
			log.Err(err).Msg("failed to ingest preprocess")
		}
	}
}

// waitConnected waits while the connection is down, and reports false if the worker is stopped meanwhile. While it is
// down, the client buffers the request of a fetch and the fetch times out all the same, so a worker fetching meanwhile
// would buffer a request per fetchWait; on reconnect they would all reach the server at once and deliver a task each
// to this one worker, the tasks waiting in the client while their AckWait runs, to be delivered again to another
// worker. Waiting leaves at most the request sent as the connection dropped: a second task, which waits for at most
// taskTimeout, far less than AckWait. NatsConn is nil in the tests that never lose the connection.
func (w *Worker) waitConnected() bool {
	for w.NatsConn != nil && w.NatsConn.IsReconnecting() {
		select {
		case <-w.stopping:
			return false
		case <-time.After(w.conf.fetchWait / 10):
		}
	}
	return true
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

	taskCtx, cancelTask := context.WithTimeout(ctx, w.conf.taskTimeout)
	defer cancelTask()

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

	err = w.handle(taskCtx, reportTask)
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
		// the pattern of no drops has no elements: inserting none is a syntax error
		if created && len(report.Drops) > 0 {
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
