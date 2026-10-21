package v2

import (
	"context"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	cachemiddleware "github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

// ErrIntervalLengthTooSmall is returned when the interval length is invalid
var ErrIntervalLengthTooSmall = pgerr.ErrInvalidReq.Msg("interval length must be greater than 1 hour")

// ErrAdvancedQueryTimeout is returned when an advanced query request ran out of advancedQueryTimeout
var ErrAdvancedQueryTimeout = pgerr.ErrInvalidReq.Msg("query timed out: the request ran longer than %d seconds and was cancelled. Please narrow down the time ranges of its queries", int(advancedQueryTimeout.Seconds()))

const (
	// advancedQueryConcurrency is how many advanced query requests the process runs at the same time. Each of them
	// scans drop reports for as long as its time range requires, and a few running together saturate the database
	// I/O, which stalls report ingestion.
	advancedQueryConcurrency = 2

	// advancedQueryConcurrencyWait is how long an advanced query request waits for a running one to finish before it
	// is rejected.
	advancedQueryConcurrencyWait = 5 * time.Second

	// advancedQueryTimeout is how long the queries of an advanced query request run in total. Each of them runs with
	// what is left of it as the statement timeout, after which PostgreSQL cancels a statement: neither a client
	// disconnecting nor a context ending stops a running statement on the server. As each statement of a query can
	// take what is left, and a query runs two aggregations, a request runs for at most about twice as long.
	advancedQueryTimeout = 60 * time.Second
)

type Result struct {
	fx.In

	DropMatrixService    *service.DropMatrix
	DropReportService    *service.DropReport
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	AccountService       *service.Account
	ItemService          *service.Item
	StageService         *service.Stage
}

func RegisterResult(v2 *svr.V2, c Result) {
	group := v2.Group("/result")

	// Cache requests with itemFilter and stageFilter as there appears to be an unknown source requesting
	// with such behaviors very eagerly, causing a relatively high load on the database.
	log.Info().Msg("enabling fiber-level cache & limiter for requests under /result group which contain itemFilter or stageFilter query params.")

	group.Use(limiter.New(limiter.Config{
		Next: func(c *fiber.Ctx) bool {
			if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
				return false
			}
			return true
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    "TOO_MANY_REQUESTS",
				"message": "Your client is sending requests too frequently. The Penguin Stats result matrix are updated periodically and should not be requested too frequently.",
			})
		},
		Max:        300,
		Expiration: time.Minute * 5,
	}))

	group.Use(cachemiddleware.New(cachemiddleware.Config{
		Next: func(c *fiber.Ctx) bool {
			// never cache personal responses: they hold the results of the account in the Authorization header,
			// which the key leaves out
			if isPersonal, _ := strconv.ParseBool(c.Query("is_personal", "false")); isPersonal {
				return true
			}
			// only cache requests with itemFilter and stageFilter query params
			if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
				return false
			}
			return true
		},
		CacheHeader:  constant.CacheHeader,
		CacheControl: true,
		Expiration:   time.Minute * 5,
		KeyGenerator: func(c *fiber.Ctx) string {
			return utils.CopyString(c.OriginalURL())
		},
	}))

	group.Get("/matrix", middlewares.ValidateServerAsQuery, c.GetDropMatrix)
	group.Get("/pattern", middlewares.ValidateServerAsQuery, c.GetPatternMatrix)
	group.Get("/trends", middlewares.ValidateServerAsQuery, c.GetTrends)
	group.Post("/advanced", limiter.New(limiter.Config{
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    "TOO_MANY_REQUESTS",
				"message": "Your client is sending requests too frequently. The Penguin Stats advanced query API is limited to 30 requests per 5 minutes.",
			})
		},
		Max:        30,
		Expiration: time.Minute * 5,
	}), middlewares.ConcurrencyLimit(advancedQueryConcurrency, advancedQueryConcurrencyWait, rejectAdvancedQuery), observeAdvancedQuery, c.AdvancedQuery)
}

//	@Summary	Get Drop Matrix
//	@Tags		Result
//	@Produce	json
//	@Param		server				query		string							true	"Server; default to CN"	Enums(CN, US, JP, KR)
//	@Param		is_personal			query		bool							false	"Whether to query for personal drop matrix or not. If `is_personal` equals to `true`, a valid PenguinID would be required to be provided (PenguinIDAuth)"
//	@Param		show_closed_zones	query		bool							false	"Whether to show closed stages or not"
//	@Param		category			query		string							false	"Category; default to all"						Enums(all, automated, manual)
//	@Param		stageFilter			query		[]string						false	"Comma separated list of stage IDs to filter"	collectionFormat(csv)
//	@Param		itemFilter			query		[]string						false	"Comma separated list of item IDs to filter"	collectionFormat(csv)
//	@Success	200					{object}	modelv2.DropMatrixQueryResult	"Drop Matrix response"
//	@Failure	500					{object}	pgerr.PenguinError				"An unexpected error occurred"
//	@Security	PenguinIDAuth
//	@Router		/PenguinStats/api/v2/result/matrix [GET]
func (c *Result) GetDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}
	showClosedZones, err := strconv.ParseBool(ctx.Query("show_closed_zones", "false"))
	if err != nil {
		return err
	}
	sourceCategory := ctx.Query("category", constant.SourceCategoryAll)
	if err := rekuest.ValidCategory(ctx, sourceCategory); err != nil {
		return err
	}
	stageFilterStr := ctx.Query("stageFilter")
	itemFilterStr := ctx.Query("itemFilter")

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimQueryResult, err := c.DropMatrixService.GetShimDropMatrix(ctx.UserContext(), server, showClosedZones, stageFilterStr, itemFilterStr, accountId, sourceCategory)
	if err != nil {
		return err
	}

	useCache := stageFilterStr == "" && itemFilterStr == ""
	if accountId.Valid {
		cachectrl.OptOut(ctx)
	} else if useCache {
		key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + constant.SourceCategoryAll
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimQueryResult)
}

//	@Summary	Get Pattern Matrix
//	@Tags		Result
//	@Produce	json
//	@Param		server			query		string	true	"Server; default to CN"	Enums(CN, US, JP, KR)
//	@Param		is_personal		query		bool	false	"Whether to query for personal drop matrix or not. If `is_personal` equals to `true`, a valid PenguinID would be required to be provided (PenguinIDAuth)"
//	@Param		showAllPatterns	query		bool	false	"Show all patterns; default to false"
//	@Success	200				{object}	modelv2.PatternMatrixQueryResult
//	@Failure	500				{object}	pgerr.PenguinError	"An unexpected error occurred"
//	@Security	PenguinIDAuth
//	@Router		/PenguinStats/api/v2/result/pattern [GET]
func (c *Result) GetPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	showAllPatterns := ctx.Query("show_all_patterns", "false") == "true"

	isPersonal, err := strconv.ParseBool(ctx.Query("is_personal", "false"))
	if err != nil {
		return err
	}

	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	shimResult, err := c.PatternMatrixService.GetShimPatternMatrix(ctx.UserContext(), server, accountId, constant.SourceCategoryAll, showAllPatterns)
	if err != nil {
		return err
	}

	if accountId.Valid {
		cachectrl.OptOut(ctx)
	} else {
		key := server + constant.CacheSep + constant.SourceCategoryAll + constant.CacheSep + strconv.FormatBool(showAllPatterns)
		var lastModifiedTime time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+key+"]", &lastModifiedTime); err != nil {
			lastModifiedTime = time.Now()
		}
		cachectrl.OptIn(ctx, lastModifiedTime)
	}

	return ctx.JSON(shimResult)
}

//	@Summary	Get Trends
//	@Tags		Result
//	@Produce	json
//	@Param		server	query		string	true	"Server; default to CN"	Enums(CN, US, JP, KR)
//	@Success	200		{object}	modelv2.TrendQueryResult
//	@Failure	500		{object}	pgerr.PenguinError	"An unexpected error occurred"
//	@Router		/PenguinStats/api/v2/result/trends [GET]
func (c *Result) GetTrends(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	shimResult, err := c.TrendService.GetShimTrend(ctx.UserContext(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimTrend#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)

	return ctx.JSON(shimResult)
}

//	@Summary	Execute Advanced Query
//	@Tags		Result
//	@Produce	json
//	@Param		query	body		types.AdvancedQueryRequest														true	"Query"
//	@Success	200		{object}	modelv2.AdvancedQueryResult{advanced_results=[]modelv2.DropMatrixQueryResult}	"Drop Matrix Response: when `interval` has been left undefined."
//	@Success	202		{object}	modelv2.AdvancedQueryResult{advanced_results=[]modelv2.TrendQueryResult}		"Trend Response: when `interval` has been defined a value greater than `0`. Notice that this response still responds with a status code of `200`, but due to swagger limitations, to denote a different response with the same status code is not possible. Therefore, a status code of `202` is used, only for the purpose of workaround."
//	@Failure	500		{object}	pgerr.PenguinError																"An unexpected error occurred"
//	@Router		/PenguinStats/api/v2/result/advanced [POST]
func (c *Result) AdvancedQuery(ctx *fiber.Ctx) error {
	var request types.AdvancedQueryRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	observability.AdvancedQueryQueriesPerRequest.Observe(float64(len(request.Queries)))
	result := &modelv2.AdvancedQueryResult{
		AdvancedResults: make([]any, 0),
	}
	deadline := time.Now().Add(advancedQueryTimeout)
	for _, query := range request.Queries {
		oneResult, err := c.runAdvancedQuery(ctx, query, deadline)
		if err != nil {
			return err
		}
		result.AdvancedResults = append(result.AdvancedResults, oneResult)
	}
	return ctx.JSON(result)
}

const (
	advancedQueryOutcomeOK          = "ok"
	advancedQueryOutcomeClientError = "client_error"
	advancedQueryOutcomeError       = "error"
	// advancedQueryOutcomeRejected is a request turned away by the concurrency limit in front of observeAdvancedQuery
	advancedQueryOutcomeRejected = "rejected"
	// advancedQueryOutcomeTimeout is a request answered with ErrAdvancedQueryTimeout, a 400 told apart from the other
	// client errors as the request did not fit in advancedQueryTimeout
	advancedQueryOutcomeTimeout = "timeout"
)

// rejectAdvancedQuery answers an advanced query request that found every slot of the concurrency limit busy for
// advancedQueryConcurrencyWait with a 429. It never reaches observeAdvancedQuery, so it is counted here, with the time
// since the request came in, most of it spent waiting for a slot, as its duration.
func rejectAdvancedQuery(ctx *fiber.Ctx) error {
	observability.AdvancedQueryDuration.WithLabelValues(advancedQueryOutcomeRejected).Observe(time.Since(ctx.Context().Time()).Seconds())
	observability.AdvancedQueryRequests.WithLabelValues(advancedQueryOutcomeRejected).Inc()
	return ctx.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"code":    "TOO_MANY_REQUESTS",
		"message": "The Penguin Stats advanced query API is busy serving other queries. Please retry later.",
	})
}

// observeAdvancedQuery records the advanced query requests in flight, and the duration and outcome of each of them.
// A request counts as in flight from here on, including any time it spends in later middlewares before the handler.
// The concurrency limit goes in front of it, so a request waiting for a slot is not in flight yet and its wait is not
// part of its duration.
func observeAdvancedQuery(ctx *fiber.Ctx) (err error) {
	observability.AdvancedQueryInFlight.Inc()
	start := time.Now()
	defer func() {
		observability.AdvancedQueryInFlight.Dec()
		r := recover()
		outcome := advancedQueryOutcomeError
		if r == nil {
			outcome = advancedQueryOutcome(ctx, err)
		}
		observability.AdvancedQueryDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
		observability.AdvancedQueryRequests.WithLabelValues(outcome).Inc()
		if r != nil {
			// let the recover middleware answer the panic with a 500 as before
			panic(r)
		}
	}()
	return ctx.Next()
}

// advancedQueryOutcome classifies a request by the status its client is answered with: the status already written
// when no error is returned, or else the status httpserver.ErrorHandler answers err with. A 4xx is a client error,
// a 5xx is an error. ErrAdvancedQueryTimeout, though a 400, is a timeout.
func advancedQueryOutcome(ctx *fiber.Ctx, err error) string {
	if errors.Is(err, ErrAdvancedQueryTimeout) {
		return advancedQueryOutcomeTimeout
	}
	status := fiber.StatusInternalServerError
	switch e := err.(type) {
	case nil:
		status = ctx.Response().StatusCode()
	case *pgerr.PenguinError:
		status = e.StatusCode
	case *fiber.Error:
		status = e.Code
	}
	switch {
	case status >= 500:
		return advancedQueryOutcomeError
	case status >= 400:
		return advancedQueryOutcomeClientError
	default:
		return advancedQueryOutcomeOK
	}
}

// runAdvancedQuery runs one query of an advanced query request, with the time left until deadline as its statement
// timeout.
func (c *Result) runAdvancedQuery(ctx *fiber.Ctx, query *types.AdvancedQuery, deadline time.Time) (any, error) {
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return nil, ErrAdvancedQueryTimeout
	}
	var result any
	err := c.DropReportService.RunWithStatementTimeout(ctx.UserContext(), timeout, func(qctx context.Context) (err error) {
		result, err = c.handleAdvancedQuery(ctx, qctx, query)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// handleAdvancedQuery runs one query of an advanced query request. qctx is the context its queries run with.
func (c *Result) handleAdvancedQuery(ctx *fiber.Ctx, qctx context.Context, query *types.AdvancedQuery) (any, error) {
	// handle isPersonal (might be null) and account
	isPersonal := false
	if query.IsPersonal.Valid {
		isPersonal = query.IsPersonal.Bool
	}
	accountId := null.NewInt(0, false)
	if isPersonal {
		account, err := c.AccountService.GetAccountFromRequest(ctx)
		if err != nil {
			return nil, err
		}
		accountId.Int64 = int64(account.AccountID)
		accountId.Valid = true
	}

	// handle start time (might be null)
	startTimeMilli := constant.ServerStartTimeMapMillis[query.Server]
	if query.StartTime != 0 {
		startTimeMilli = query.StartTime
	}
	startTime := time.UnixMilli(startTimeMilli)

	// handle end time (might be null)
	endTimeMilli := time.Now().UnixMilli()
	if query.EndTime != 0 {
		endTimeMilli = query.EndTime
	}
	endTime := time.UnixMilli(endTimeMilli)

	// handle ark stage id
	stage, err := c.StageService.GetStageByArkId(qctx, query.StageID)
	if err != nil {
		return nil, err
	}

	// handle item ids
	itemIds := make([]int, 0)
	for _, arkItemID := range query.ItemIDs {
		item, err := c.ItemService.GetItemByArkId(qctx, arkItemID)
		if err != nil {
			return nil, err
		}
		itemIds = append(itemIds, item.ItemID)
	}

	// handle sourceCategory, default to all
	sourceCategory := query.SourceCategory
	if sourceCategory == "" {
		sourceCategory = constant.SourceCategoryAll
	}

	// if there is no interval, then do drop matrix query, otherwise do trend query
	if !query.Interval.Valid {
		timeRange := &model.TimeRange{
			StartTime: &startTime,
			EndTime:   &endTime,
		}
		return c.DropMatrixService.GetShimCustomizedDropMatrixResults(qctx, query.Server, timeRange, []int{stage.StageID}, itemIds, accountId, sourceCategory)
	} else {
		// interval originally is in milliseconds, so we need to convert it to nanoseconds
		intervalLength := time.Duration(query.Interval.Int64 * 1e6).Round(time.Hour)
		if intervalLength.Hours() < 1 {
			return nil, ErrIntervalLengthTooSmall
		}
		intervalNum := c.calcIntervalNum(startTime, endTime, intervalLength)
		if intervalNum > constant.MaxIntervalNum {
			return nil, pgerr.ErrInvalidReq.Msg("too many sections: interval number is %d sections, which is larger than %d sections", intervalNum, constant.MaxIntervalNum)
		}

		shimTrendQueryResult, err := c.TrendService.GetShimCustomizedTrendResults(qctx, query.Server, &startTime, intervalLength, intervalNum, []int{stage.StageID}, itemIds, accountId, sourceCategory)
		if err != nil {
			return nil, err
		}
		return shimTrendQueryResult, nil
	}
}

func (c *Result) calcIntervalNum(startTime, endTime time.Time, intervalLength time.Duration) int {
	diff := endTime.Sub(startTime)
	// implicit float64 to int: drops fractional part (truncates towards 0)
	return int(diff.Hours()) / int(intervalLength.Hours())
}
