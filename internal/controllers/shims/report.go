package shims

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
)

type ReportController struct {
	fx.In

	Crypto        *utils.Crypto
	ReportService *service.ReportService
}

func RegisterReportController(v2 *server.V2, v3 *server.V3, c ReportController) {
	v2.Post("/report", c.SingularReport)
	v2.Post("/report/recall", c.RecallSingularReport)
	v2.Post("/report/recognition", c.RecognitionReport)
}

// @Summary      Submit an Item Drop Report
// @Description
// @Tags         Report
// @Accept	     json
// @Produce      json
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/report [POST]
func (c *ReportController) SingularReport(ctx *fiber.Ctx) error {
	var report types.SingleReportRequest
	if err := rekuest.ValidBody(ctx, &report); err != nil {
		return err
	}

	taskId, err := c.ReportService.PreprocessAndQueueSingularReport(ctx, &report)
	if err != nil {
		return err
	}
	return ctx.JSON(shims.ReportResponse{ReportHash: taskId})
}

// @Summary      Recall an Item Drop Report
// @Description
// @Tags         Report
// @Accept	     json
// @Produce      json
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/report [POST]
func (c *ReportController) RecallSingularReport(ctx *fiber.Ctx) error {
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
// @Description  Submit an Item Drop Report with Frontend Recognition. Notice that this is a private API and is not designed for external use.
// @Tags         Report
// @Produce      json
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/report/recognition [POST]
func (c *ReportController) RecognitionReport(ctx *fiber.Ctx) error {
	encrypted := string(ctx.Body())

	segments := strings.SplitN(encrypted, ":", 2)

	if err := rekuest.Validate.Var(segments, "len=2"); err != nil {
		log.Warn().
			Err(err).
			Msg("failed to decrypt recognition request")
		return errors.ErrInvalidReq
	}

	privateKey := segments[0]
	body := segments[1]

	decrypted, err := c.Crypto.Decrypt(privateKey, body)
	if err != nil {
		log.Warn().
			Err(err).
			Msg("failed to decrypt recognition request")
		return errors.ErrInvalidReq
	}

	var request types.BatchReportRequest
	if err = json.Unmarshal(decrypted, &request); err != nil {
		log.Warn().
			Err(err).
			Msg("failed to unmarshal recognition request")
		return errors.ErrInvalidReq
	}

	if e := log.Trace(); e.Enabled() {
		e.Str("request", string(decrypted)).
			Msg("received recognition report request")
	}

	_, err = c.ReportService.PreprocessAndQueueBatchReport(ctx, &request)
	if err != nil {
		return err
	}

	return ctx.JSON(request)
}
