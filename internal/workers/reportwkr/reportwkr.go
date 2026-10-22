package reportwkr

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
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
	"go.opentelemetry.io/otel/attribute"
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
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportutil"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

var tracer = otel.Tracer("reportwkr")

// The attributes of the spans of a report task besides observability.ReportTaskAttributes.
const (
	// the deliveries of the task's message so far, this one included (messaging.nats.num_delivered)
	attributeNumDelivered = attribute.Key("messaging.nats.num_delivered")
	// how long the task waited in the stream before this delivery, in milliseconds
	attributeQueueLatency = attribute.Key("report.queue_latency_ms")
	// the verdict of the verifiers on the task's reports: 0 if they accepted every report, else the reliability given
	// the first one rejected (the reliability of its drop_reports row); and the number of reports rejected
	attributeReliability = attribute.Key("report.reliability")
	attributeRejected    = attribute.Key("report.rejected")
	// why reports were rejected: the messages of the verifiers that rejected them, joined (at most maxViolationsLen)
	attributeViolations = attribute.Key("report.violations")
	// the id of the last report the task stored (drop_reports.report_id)
	attributeReportID = attribute.Key("report.id")
)

// maxViolationsLen bounds the report.violations attribute: a batch can have many reports rejected.
const maxViolationsLen = 512

// reliabilityAttributes returns the attributes of the verdict of the verifiers on a task's reports.
func reliabilityAttributes(reliabilities []int, violations []string) []attribute.KeyValue {
	reliability := 0
	for _, r := range reliabilities {
		if r != 0 {
			reliability = r
			break
		}
	}
	attrs := []attribute.KeyValue{attributeReliability.Int(reliability), attributeRejected.Int(len(violations))}
	if len(violations) > 0 {
		v := strings.Join(violations, "; ")
		if len(v) > maxViolationsLen {
			v = strings.ToValidUTF8(v[:maxViolationsLen], "")
		}
		attrs = append(attrs, attributeViolations.String(v))
	}
	return attrs
}

// endSpan ends span with the status of the work it covered: the error, as an exception event and the status
// description (Datadog's error.message and error.type), if it failed.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

// rawTaskKey is the context key of the report task as published (a pointer to the message data, which a context
// holds without an allocation), which process logs rather than encoding the decoded task again
type rawTaskKey struct{}

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
	DropInfoRepo           *repo.DropInfo
	RejectRuleRepo         *repo.RejectRule
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
	// a failed task is delivered again after nakDelay times its deliveries so far, up to maxNakDelay
	nakDelay    time.Duration
	maxNakDelay time.Duration
	// deadLetterAfter is the delivery whose failure moves a task to the dead-letter stream instead of retrying it
	deadLetterAfter uint64
	// deadSubject is the subject prefix of the dead-letter stream: a task of REPORT.SINGLE moves to
	// <deadSubject>SINGLE
	deadSubject string
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
	nakDelay:      time.Second * 15,
	maxNakDelay:   time.Minute * 5,
	// The 30th delivery comes 97.5 minutes of retry delays after the first (TestDeadLetterAfter), so a database or
	// Redis outage of up to about an hour and a half is retried through, while a task that can never be stored leaves
	// the MaxAckPending window that it shares with the healthy tasks.
	deadLetterAfter: 30,
	deadSubject:     "REPORT_DEAD.",
}

// The headers of a task in the dead-letter stream.
const (
	HeaderOriginalSubject  = "Penguin-Original-Subject"
	HeaderOriginalSequence = "Penguin-Original-Sequence"
	HeaderDeliveries       = "Penguin-Deliveries"
	HeaderLastError        = "Penguin-Last-Error"
)

type Worker struct {
	WorkerDeps

	conf     consumerConfig
	verifier reportverifs.Verifier
	// reportSchemaV2 writes drop_report_extras.created_at and keeps the report partitions: see
	// appconfig.ConfigSpec.ReportSchemaV2Enabled
	reportSchemaV2 bool
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
		verifier:   reportverifs.Verifier{DropInfoRepo: deps.DropInfoRepo, RejectRuleRepo: deps.RejectRuleRepo},
		stopping:   make(chan struct{}),
	}
	w.handle = w.process
	return w
}

// Start checks that the database has the report schema of the configuration and sets the report consumer up, failing
// the startup if either fails, and runs its workers while the app runs.
func Start(lc fx.Lifecycle, conf *appconfig.Config, deps WorkerDeps) error {
	w := newWorker(deps, defaultConsumerConfig)
	w.reportSchemaV2 = conf.ReportSchemaV2Enabled
	if err := w.checkReportSchema(context.Background()); err != nil {
		return err
	}
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

// start runs a worker per subscription, and on the new database the upkeep of the report partitions.
func (w *Worker) start() {
	if w.reportSchemaV2 {
		w.wg.Add(1)
		go w.maintainReportPartitions()
	}
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
			// no task queued, or the request has not reached the server yet
			if !w.waitFlushed() {
				return
			}
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

		w.ingestPreprocess(msgs[0])
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

// waitFlushed waits until the server has received what the client sent, the request of the fetch that timed out
// included, and reports false if the worker is stopped meanwhile. A connection can stay up while its traffic stalls,
// for example behind a stuck proxy: each fetch then times out in the client while its request waits on the way, and
// the server receives every request at once when the traffic resumes, each from that moment on, to deliver a task each
// to this one worker. Fetching only once the requests sent before have arrived leaves at most the request of the last
// fetch still running on the server: a second task, as in waitConnected.
func (w *Worker) waitFlushed() bool {
	for w.NatsConn != nil {
		if !w.waitConnected() {
			return false
		}
		if err := w.NatsConn.FlushTimeout(w.conf.fetchWait); err == nil {
			return true
		}
		select {
		case <-w.stopping:
			return false
		case <-time.After(w.conf.fetchWait / 10):
		}
	}
	return true
}

// ingestPreprocess handles a delivered task and settles its message by the outcome. A task processed is acked; a task
// that can never be processed, or that failed at its deadLetterAfter-th delivery, is moved to the dead-letter stream
// (park); any other failure is delivered again later. Each failure is logged once, here.
func (w *Worker) ingestPreprocess(msg *nats.Msg) {
	outcome := "failed_process"
	defer func() {
		observability.ReportConsumeTotal.
			WithLabelValues(outcome, observability.ReportSubjectLabel(msg.Subject)).
			Inc()
	}()

	metadata, err := msg.Metadata()
	if err != nil {
		// should not happen: the message should be always a jetstream message
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.malformed").
			Str("subject", msg.Subject).
			Bytes("data", msg.Data).
			Msg("failed to get report task message metadata; terminating")
		// no stream sequence to record it under; a JetStream delivery always has one
		term(msg)
		return
	}
	observability.ReportConsumeNumDelivered.Observe(float64(metadata.NumDelivered))

	reportTask := &types.ReportTask{}
	if err := json.Unmarshal(msg.Data, reportTask); err != nil {
		outcome = "failed_unmarshal"
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.malformed").
			Str("subject", msg.Subject).
			Uint64("seq", metadata.Sequence.Stream).
			Bytes("data", msg.Data).
			Msg("failed to decode report task; moving it to the dead-letter stream")
		w.park(msg, metadata, err, "malformed")
		return
	}

	if metadata.NumDelivered > w.conf.deadLetterAfter {
		// moved to the dead-letter stream at an earlier delivery whose Term was lost, or whose move failed: it is not
		// processed again, which would store it while its copy waits there to be replayed
		err := errors.Errorf("delivered %d times, more than the %d after which a task moves to the dead-letter stream", metadata.NumDelivered, w.conf.deadLetterAfter)
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.dead").
			Str("taskId", reportTask.TaskID).
			Uint64("delivered", metadata.NumDelivered).
			Uint64("seq", metadata.Sequence.Stream).
			RawJSON("reportTask", msg.Data).
			Msg("report task moving to the dead-letter stream")
		w.park(msg, metadata, err, "over_delivered")
		return
	}

	taskCtx, cancelTask := context.WithTimeout(context.Background(), w.conf.taskTimeout)
	defer cancelTask()

	start := time.Now()
	defer func() {
		observability.ReportConsumeDuration.
			WithLabelValues().
			Observe(time.Since(start).Seconds())
	}()

	// a child of the publish span of the task (the trace context in its headers), in the trace of the request that
	// published it, and sampled as it is; a task published without one starts a trace of its own
	var span trace.Span
	taskCtx, span = tracer.
		Start(jetstream.ExtractTraceContext(taskCtx, msg.Header), "reportwkr.ConsumeTask",
			trace.WithSpanKind(trace.SpanKindConsumer))
	// set only on a sampled span: building them costs the tasks that are not sampled, most of them, allocations
	if span.IsRecording() {
		attrs := append(observability.ReportTaskAttributes(reportTask),
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationNameKey.String(msg.Subject),
			semconv.MessagingMessageIDKey.String(jetstream.MessageID(metadata.Sequence)),
			semconv.MessagingMessagePayloadSizeBytesKey.Int(len(msg.Data)),
			// 1 for a first delivery; more for a task retried after a failure, or redelivered after AckWait
			attributeNumDelivered.Int64(int64(metadata.NumDelivered)),
			// how long the task waited in the stream, from its publishing to this delivery
			attributeQueueLatency.Float64(float64(time.Since(time.UnixMicro(reportTask.CreatedAt)).Microseconds())/1000),
		)
		span.SetAttributes(attrs...)
	}

	err = w.handle(context.WithValue(taskCtx, rawTaskKey{}, &msg.Data), reportTask)
	endSpan(span, err)

	// the lines below link to the task's trace, if it is sampled
	sc := span.SpanContext()
	switch {
	case err == nil:
		observability.TraceIDs(log.Info(), sc).
			Str("evt.name", "reportwkr.processed").
			Str("taskId", reportTask.TaskID).
			RawJSON("task", msg.Data).
			Dur("duration", time.Since(start)).
			Msg("report task processed successfully")

		outcome = "stored"
		if err := msg.AckSync(); err != nil {
			observability.ReportConsumeAckFailures.Inc()
			// the task is stored, and is stored again if the server delivers it again after AckWait (rare: the ack
			// failed while the connection or the server was down)
			log.Error().
				Err(err).
				Str("evt.name", "reportwkr.ack.failed").
				Str("taskId", reportTask.TaskID).
				Msg("failed to ack report task")
		}
	case errors.Is(err, pgerr.ErrNotFound):
		// a stage of the task does not exist (process looks nothing else up by key): no retry can store it. The task
		// is logged as it was published: process changes its drops and ip in place.
		observability.TraceIDs(log.Error(), sc).
			Err(err).
			Str("evt.name", "reportwkr.parked").
			Str("taskId", reportTask.TaskID).
			Uint64("seq", metadata.Sequence.Stream).
			RawJSON("reportTask", msg.Data).
			Msg("report task parked: it can never be processed; moving it to the dead-letter stream")
		w.park(msg, metadata, err, "not_found")
	case metadata.NumDelivered >= w.conf.deadLetterAfter:
		// failed at every delivery for over an hour and a half of retries: most likely it can never be stored, and it
		// would hold a slot of the MaxAckPending window that it shares with the healthy tasks for as long as it is
		// retried
		observability.TraceIDs(log.Error(), sc).
			Err(err).
			Str("evt.name", "reportwkr.dead").
			Str("taskId", reportTask.TaskID).
			Uint64("delivered", metadata.NumDelivered).
			Uint64("seq", metadata.Sequence.Stream).
			RawJSON("reportTask", msg.Data).
			Msg("report task failed at its last delivery; moving it to the dead-letter stream")
		w.park(msg, metadata, err, "max_deliveries")
	default:
		// a database, Redis or network failure, or a commit of unknown outcome (stored again if it did commit, rare
		// against losing the task): retry later, waiting longer with each delivery, up to deadLetterAfter deliveries
		delivered := metadata.NumDelivered
		delay := w.conf.retryDelay(delivered)
		ev := log.Warn()
		if delivered > 5 {
			ev = log.Error()
		}
		observability.TraceIDs(ev, sc).
			Err(err).
			Str("evt.name", "reportwkr.failed").
			Str("taskId", reportTask.TaskID).
			Uint64("delivered", delivered).
			Dur("retryIn", delay).
			Uint64("seq", metadata.Sequence.Stream).
			RawJSON("reportTask", msg.Data).
			Msg("failed to consume report task")

		nak(msg, delay)
	}
}

// park moves a task that will not be processed to the dead-letter stream, then terminates it. If the dead-letter
// stream does not store it, the task is delivered again later instead, so that it is never dropped. reason labels
// ReportDeadLetteredTotal.
func (w *Worker) park(msg *nats.Msg, metadata *nats.MsgMetadata, cause error, reason string) {
	withHeaders, duplicate, err := w.deadLetter(msg, metadata, cause)
	if err != nil {
		delay := w.conf.retryDelay(metadata.NumDelivered)
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.dead.failed").
			Str("subject", msg.Subject).
			Uint64("seq", metadata.Sequence.Stream).
			Dur("retryIn", delay).
			Msg("failed to move report task to the dead-letter stream; delivering it again later")
		nak(msg, delay)
		return
	}
	if !withHeaders {
		log.Error().
			Err(cause).
			Str("evt.name", "reportwkr.dead.bare").
			Str("subject", msg.Subject).
			Uint64("seq", metadata.Sequence.Stream).
			Uint64("delivered", metadata.NumDelivered).
			Msg("report task moved to the dead-letter stream without its headers: with them it would exceed the maximum payload")
	}
	// a duplicate is the copy of an earlier delivery whose Term was lost: the task is counted once
	if !duplicate {
		observability.ReportDeadLetteredTotal.WithLabelValues(reason).Inc()
	}
	term(msg)
}

// deadLetter copies msg to the dead-letter stream, with its subject, stream sequence, deliveries and the error that
// put it there in headers, and returns once the stream has stored it, or failed to. The copy is deduplicated by the
// stream sequence of the task, so a task moved twice (its Term lost) is stored there once.
//
// The maximum payload of NATS counts the headers too, so a task that the stream accepted just under it would not fit
// with them, and could never be moved: such a task is copied with its payload only (withHeaders false), which fits as
// the stream accepted it, and its log is the record of the rest. duplicate reports that the stream already held the
// copy, which it then does not store again.
func (w *Worker) deadLetter(msg *nats.Msg, metadata *nats.MsgMetadata, cause error) (withHeaders, duplicate bool, err error) {
	dead := nats.NewMsg(w.conf.deadSubject + msg.Subject[strings.LastIndexByte(msg.Subject, '.')+1:])
	dead.Data = msg.Data
	dead.Header.Set(nats.MsgIdHdr, fmt.Sprintf("%s:%d", metadata.Stream, metadata.Sequence.Stream))
	dead.Header.Set(HeaderOriginalSubject, msg.Subject)
	dead.Header.Set(HeaderOriginalSequence, strconv.FormatUint(metadata.Sequence.Stream, 10))
	dead.Header.Set(HeaderDeliveries, strconv.FormatUint(metadata.NumDelivered, 10))
	dead.Header.Set(HeaderLastError, headerValue(cause.Error()))
	withHeaders = true
	if w.NatsConn != nil && int64(headerSize(dead.Header)+len(dead.Data)) > w.NatsConn.MaxPayload() {
		dead.Header = nats.Header{}
		withHeaders = false
	}

	// synchronous, bounded by taskTimeout: an asynchronous future whose ack never arrives (lost to a reconnect) stays
	// pending in the JetStream context, which report publishing shares, until the connection closes, while a request
	// that times out is removed from the connection. The connection subscribes to an inbox per request, so a send that
	// fails at once leaves nothing behind either (infra.NATSOptions).
	ctx, cancel := context.WithTimeout(context.Background(), w.conf.taskTimeout)
	defer cancel()
	ack, err := w.NatsJS.PublishMsg(dead, nats.Context(ctx))
	if err != nil {
		return withHeaders, false, errors.Wrap(err, "the dead-letter stream did not store the task")
	}
	return withHeaders, ack != nil && ack.Duplicate, nil
}

// headerSize returns the size of h on the wire, as NATS counts it against the maximum payload.
func headerSize(h nats.Header) int {
	n := len("NATS/1.0\r\n") + len("\r\n")
	for k, vs := range h {
		for _, v := range vs {
			n += len(k) + len(": ") + len(v) + len("\r\n")
		}
	}
	return n
}

// headerValue makes s a header value: on one line, and at most 1024 bytes.
func headerValue(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	if len(s) > 1024 {
		s = strings.ToValidUTF8(s[:1024], "")
	}
	return s
}

// nak tells the server to deliver the message again after delay.
func nak(msg *nats.Msg, delay time.Duration) {
	if err := msg.NakWithDelay(delay); err != nil {
		observability.ReportConsumeAckFailures.Inc()
		// delivered again after AckWait instead
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.nak.failed").
			Str("subject", msg.Subject).
			Msg("failed to nak report task")
	}
}

// retryDelay returns how long a task that failed at its delivered-th delivery waits before it is delivered again.
func (c *consumerConfig) retryDelay(delivered uint64) time.Duration {
	delay := time.Duration(delivered) * c.nakDelay
	if delay > c.maxNakDelay {
		delay = c.maxNakDelay
	}
	return delay
}

// term tells the server never to deliver the message again.
func term(msg *nats.Msg) {
	if err := msg.Term(); err != nil {
		observability.ReportConsumeAckFailures.Inc()
		log.Error().
			Err(err).
			Str("evt.name", "reportwkr.term.failed").
			Str("subject", msg.Subject).
			Msg("failed to terminate report task")
	}
}

func (w *Worker) process(ctx context.Context, reportTask *types.ReportTask) error {
	lc := log.With()
	if raw, ok := ctx.Value(rawTaskKey{}).(*[]byte); ok {
		lc = lc.RawJSON("task", *raw)
	} else {
		lc = lc.Interface("task", reportTask)
	}
	L := lc.Logger()

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

	// verified before persisting: persist merges the drops of each report by item, in place
	reliabilities := make([]int, len(reportTask.Reports))
	// the messages of the rejected reports, joined as the pipeline's Violations were
	var violations []string
	for idx, report := range reportTask.Reports {
		reliability, message, err := w.verifier.Reliability(verifyCtx, reportTask, report)
		if err != nil {
			err = errors.Wrap(err, "failed to verify report")
			endSpan(verifySpan, err)
			return err
		}
		reliabilities[idx] = reliability
		if reliability != 0 {
			violations = append(violations, message)
		}
	}
	if len(violations) > 0 {
		L.Warn().
			Str("evt.name", "reportwkr.violations").
			Str("violations", strings.Join(violations, "; ")).
			Ints("reliabilities", reliabilities).
			Msg("report task verification failed on some or all reports")
	}
	// on the task's span (the parent), where traces are searched: @report.rejected:true
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(reliabilityAttributes(reliabilities, violations)...)
	}

	endSpan(verifySpan, nil)

	pstCtx, pstSpan := tracer.
		Start(ctx, "reportwkr.process.Persistence",
			trace.WithSpanKind(trace.SpanKindInternal))
	var err error
	defer func() { endSpan(pstSpan, err) }()

	// the transaction is rolled back when persist returns an error, and committed otherwise
	var reportId int
	var patterns []*model.DropPattern
	err = w.DB.RunInTx(pstCtx, nil, func(ctx context.Context, tx bun.Tx) (err error) {
		reportId, patterns, err = w.persist(ctx, tx, reportTask, taskCreatedAt, reliabilities)
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
	// committed: the patterns exist now
	w.DropPatternRepo.Remember(patterns...)
	if reportId != 0 && pstSpan.IsRecording() {
		trace.SpanFromContext(ctx).SetAttributes(attributeReportID.Int(reportId))
	}
	server := observability.ReportServerLabel(reportTask.Server)
	observability.ReportStoredTotal.
		WithLabelValues(observability.ReportSourceLabel(reportTask.Source), server).
		Add(float64(len(reportTask.Reports)))
	// counted once committed, as stored_total is: a report of a task rolled back is not stored
	sourceName := observability.ReportSourceNameLabel(reportTask.Source)
	for _, reliability := range reliabilities {
		observability.ReportReliability.WithLabelValues(strconv.Itoa(reliability), sourceName, server).Inc()
	}

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

// persist stores the reports of the task in tx with their reliabilities, and returns the id of the last report of
// the task (0 if it has none) and the drop patterns of its reports.
func (w *Worker) persist(ctx context.Context, tx bun.Tx, reportTask *types.ReportTask, taskCreatedAt time.Time, reliabilities []int) (int, []*model.DropPattern, error) {
	var reportId int
	patterns := make([]*model.DropPattern, 0, len(reportTask.Reports))
	// calculate drop pattern hash for each report
	for idx, report := range reportTask.Reports {
		report.Drops = reportutil.MergeDropsByItemID(report.Drops)

		dropPattern, created, err := w.DropPatternRepo.GetOrCreateDropPatternFromDrops(ctx, tx, report.Drops)
		if err != nil {
			return 0, nil, errors.Wrap(err, "failed to calculate drop pattern hash")
		}
		patterns = append(patterns, dropPattern)
		// the pattern of no drops has no elements: inserting none is a syntax error
		if created && len(report.Drops) > 0 {
			_, err := w.DropPatternElementRepo.CreateDropPatternElements(ctx, tx, dropPattern.PatternID, report.Drops)
			if err != nil {
				return 0, nil, errors.Wrap(err, "failed to create drop pattern elements")
			}
		}

		stage, err := w.StageService.GetStageByArkId(ctx, report.StageID)
		if err != nil {
			return 0, nil, errors.Wrap(err, "failed to get stage")
		}

		reliability := reliabilities[idx]

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
			return 0, nil, errors.Wrap(err, "failed to create drop report")
		}

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
		dropReportExtra := &model.DropReportExtra{
			ReportID: dropReport.ReportID,
			IP:       reportTask.IP,
			Metadata: report.Metadata,
			MD5:      null.NewString(md5, md5 != ""),
		}
		if w.reportSchemaV2 {
			// the partition key of drop_report_extras on the new database; today's has no such column
			dropReportExtra.CreatedAt = dropReport.CreatedAt
		}
		if err = w.DropReportExtraRepo.CreateDropReportExtra(ctx, tx, dropReportExtra); err != nil {
			return 0, nil, errors.Wrap(err, "failed to create drop report extra")
		}

		reportId = dropReport.ReportID
	}

	return reportId, patterns, nil
}
