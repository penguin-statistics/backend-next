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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/jetstream"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/pkg/pgid"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
	"exusiai.dev/backend-next/internal/util/reportutil"
)

var (
	ErrReportNotFound = pgerr.ErrInvalidReq.Msg("report not existed or has already been recalled")
	ErrAccountMissing = pgerr.ErrInvalidReq.Msg("account missing")
	ErrNatsTimeout    = errors.New("timeout waiting for NATS response")

	ErrReportQueueUnavailable = pgerr.New(fiber.StatusServiceUnavailable, pgerr.CodeServiceUnavailable, "report queue is temporarily unavailable: please retry later")
)

// natsPublishTimeout bounds how long commitReportTask waits for NATS to acknowledge a report task. It is a variable so
// that tests can shorten it.
var natsPublishTimeout = time.Second * 10

var tracer = otel.Tracer("report")

type Report struct {
	DB                     *bun.DB
	Redis                  *redis.Client
	NatsConn               *nats.Conn
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
}

func NewReport(db *bun.DB, redisClient *redis.Client, natsConn *nats.Conn, natsJs nats.JetStreamContext, itemService *Item, stageService *Stage, stageRepo *repo.Stage, dropInfoRepo *repo.DropInfo, dropReportRepo *repo.DropReport, dropReportExtraRepo *repo.DropReportExtra, dropPatternRepo *repo.DropPattern, dropPatternElementRepo *repo.DropPatternElement, accountService *Account, timeRangeService *TimeRange) *Report {
	service := &Report{
		DB:                     db,
		Redis:                  redisClient,
		NatsConn:               natsConn,
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
	// cached: a report is accepted without a query of the database, as long as the stage was looked up recently
	stage, err := s.StageService.GetStageByArkId(ctx, singleReport.StageID)
	if err != nil {
		return err
	}
	if stage.ExtraProcessType.Valid && stage.ExtraProcessType.String == constant.ExtraProcessTypeGachaBox {
		reportutil.AggregateGachaBoxDrops(singleReport)
	}

	return nil
}

func (s *Report) commitReportTask(ctx *fiber.Ctx, subject string, task *types.ReportTask) (taskId string, err error) {
	// While NATS is reconnecting, a publish is buffered in the client and only sent after the reconnect, which may be
	// long after we answered with ErrNatsTimeout and the client retried. Refuse it right away instead, so the client
	// gets a clear 503 and no report is left in the buffer to be stored behind its back. (NewReport always sets the
	// connection; a Report built without one, as by the publish metrics tests, publishes without the check.)
	if s.NatsConn != nil && s.NatsConn.Status() != nats.CONNECTED {
		return "", ErrReportQueueUnavailable
	}

	taskId = s.PipelineTaskId(ctx)
	task.TaskID = taskId

	reportTaskJsonBytes, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	// Wait for the stream to store the task. An asynchronous publish leaks the ack of a task given up on, and once
	// such acks reach the limit of pending ones, every publish stalls. The connection makes each request on an inbox
	// of its own (infra.NATSOptions), so a publish whose send fails at once leaks nothing either.
	start := time.Now()
	pubCtx, cancel := context.WithTimeout(ctx.UserContext(), natsPublishTimeout)
	defer cancel()
	opts := []nats.PubOpt{nats.Context(pubCtx)}
	// A request retried with its idempotency key after a timeout publishes the task again; the stream stores only the
	// first copy published within its duplicate window, unless the server restarted after a worker consumed that copy:
	// a restarted server knows the ids of the tasks it still holds only. The task id is unique per request, so it
	// cannot be the id.
	if key, ok := ctx.Locals(constant.IdempotencyKeyLocalsKey).(string); ok && key != "" {
		opts = append(opts, nats.MsgId(key))
	}
	// The task carries the trace context of its publish span, a child of the request's, in its headers (traceparent
	// and tracestate): the report worker's span of the task continues the request's trace, and is sampled as it is.
	pubCtx, span := tracer.Start(pubCtx, "report.PublishTask", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	// set only on a sampled span: building them costs the requests that are not sampled, most of them, an allocation
	if span.IsRecording() {
		span.SetAttributes(
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationNameKey.String(subject),
			semconv.MessagingMessagePayloadSizeBytesKey.Int(len(reportTaskJsonBytes)),
			observability.AttributeReportTaskID.String(taskId),
		)
		// what the request reported, on its server span: the root of the trace, where traces are searched and grouped
		// (sampled whenever this child is)
		trace.SpanFromContext(ctx.UserContext()).SetAttributes(observability.ReportTaskAttributes(task)...)
	}
	msg := &nats.Msg{Subject: subject, Data: reportTaskJsonBytes, Header: nats.Header{}}
	jetstream.InjectTraceContext(pubCtx, msg.Header)
	_, err = s.NatsJS.PublishMsg(msg, opts...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	switch {
	case err == nil:
		observeReportPublish(start, "ok")
		return taskId, nil
	case ctx.UserContext().Err() != nil:
		observeReportPublish(start, "ctx_cancelled")
		return "", ctx.UserContext().Err()
	case errors.Is(err, context.DeadlineExceeded):
		observeReportPublish(start, "timeout")
		return "", ErrNatsTimeout
	default:
		observeReportPublish(start, "error")
		return "", err
	}
}

func observeReportPublish(start time.Time, result string) {
	observability.ReportPublishDuration.Observe(time.Since(start).Seconds())
	observability.ReportPublishTotal.WithLabelValues(result).Inc()
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

	rowsAffected, err := s.DropReportRepo.DeleteDropReport(ctx, reportId)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		// the key names no stored report: tell the client, and leave the key as it is
		return ErrReportNotFound
	}

	s.Redis.Del(ctx, constant.ReportRedisPrefix+req.ReportHash)

	return nil
}
