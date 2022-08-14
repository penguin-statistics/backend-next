package v2

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/crypto"
	"github.com/penguin-statistics/backend-next/internal/pkg/fiberstore"
	"github.com/penguin-statistics/backend-next/internal/pkg/middlewares"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type Report struct {
	fx.In

	Redis         *redis.Client
	Crypto        *crypto.Crypto
	ReportService *service.Report
}

func RegisterReport(v2 *svr.V2, c Report) {
	v2.Post("/report", middlewares.Idempotency(&middlewares.IdempotencyConfig{
		Lifetime:  time.Hour * 24,
		KeyHeader: constant.IdempotencyKeyHeader,
		KeepResponseHeaders: []string{
			fiber.HeaderContentType,
			fiber.HeaderContentLength,
			fiber.HeaderSetCookie,
			constant.PenguinIDSetHeader,
			constant.ShimCompatibilityHeaderKey,
		},
		Storage: fiberstore.NewRedis(c.Redis, "report-idempotency"),
	}), c.SingularReport)
	v2.Post("/report/recall", c.RecallSingularReport)
	v2.Post("/report/recognition", c.RecognitionReport)
}

// @Summary      Submit a Drop Report
// @Description  Submit a Drop Report. You can use the `reportHash` in the response to recall the report in 24 hours after it has been submitted.
// @Tags         Report
// @Accept       json
// @Produce      json
// @Param        report  body      types.SingleReportRequest  true  "Report request"
// @Success      201     {object}  modelv2.ReportResponse     "Report has been successfully submitted"
// @Failure      400     {object}  pgerr.PenguinError         "Invalid request"
// @Failure      500     {object}  pgerr.PenguinError         "An unexpected error occurred"
// @Security     PenguinIDAuth
// @Router       /PenguinStats/api/v2/report [POST]
func (c *Report) SingularReport(ctx *fiber.Ctx) error {
	var report types.SingleReportRequest
	if err := rekuest.ValidBody(ctx, &report); err != nil {
		return err
	}

	taskId, err := c.ReportService.PreprocessAndQueueSingularReport(ctx, &report)
	if err != nil {
		return err
	}
	return ctx.JSON(modelv2.ReportResponse{ReportHash: taskId})
}

// @Summary      Recall a Drop Report
// @Description  Recall a Drop Report by its `reportHash`. The farest report you can recall is limited to 24 hours. Recalling a report after it has been already recalled will result in an error.
// @Tags         Report
// @Accept       json
// @Produce      json
// @Param        report  body  types.SingleReportRecallRequest  true  "Report Recall request"
// @Success      204     "Report has been successfully recalled"
// @Failure      400     {object}  pgerr.PenguinError  "`reportHash` is missing, invalid, or already been recalled."
// @Failure      500     {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/report/recall [POST]
func (c *Report) RecallSingularReport(ctx *fiber.Ctx) error {
	var req types.SingleReportRecallRequest
	if err := rekuest.ValidBody(ctx, &req); err != nil {
		return err
	}

	err := c.ReportService.RecallSingularReport(ctx.Context(), &req)
	if err != nil {
		return err
	}

	return ctx.SendStatus(fiber.StatusOK)
}

// @Summary      Bulk Submit with Frontend Recognition
// @Description  Submit an Item Drop Report with Frontend Recognition. Notice that this is a **private API** and is not designed for external use.
// @Tags         Report
// @Produce      json
// @Param        report  body      string                             true  "Recognition Report Request"
// @Success      200     {object}  modelv2.RecognitionReportResponse  "Report has been successfully submitted for queue processing"
// @Failure      400     {object}  pgerr.PenguinError                 "Invalid request"
// @Failure      500     {object}  pgerr.PenguinError                 "An unexpected error occurred"
// @Security     PenguinIDAuth
// @Router       /PenguinStats/api/v2/report/recognition [POST]
func (c *Report) RecognitionReport(ctx *fiber.Ctx) error {
	encrypted := string(ctx.Body())

	segments := strings.SplitN(encrypted, ":", 2)

	if err := rekuest.Validate.Var(segments, "len=2"); err != nil {
		log.Warn().
			Err(err).
			Msg("failed to decrypt recognition request")
		return pgerr.ErrInvalidReq
	}

	privateKey := segments[0]
	body := segments[1]

	decrypted, err := c.Crypto.Decrypt(privateKey, body)
	if err != nil {
		log.Warn().
			Err(err).
			Msg("failed to decrypt recognition request")
		return pgerr.ErrInvalidReq
	}

	var request types.BatchReportRequest
	if err = json.Unmarshal(decrypted, &request); err != nil {
		log.Warn().
			Err(err).
			Msg("failed to unmarshal recognition request")
		return pgerr.ErrInvalidReq
	}

	if e := log.Trace(); e.Enabled() {
		e.Str("request", string(decrypted)).
			Msg("received recognition report request")
	}

	taskId, err := c.ReportService.PreprocessAndQueueBatchReport(ctx, &request)
	if err != nil {
		return err
	}

	return ctx.JSON(modelv2.RecognitionReportResponse{
		TaskId: taskId,
		Errors: []string{},
	})
}
