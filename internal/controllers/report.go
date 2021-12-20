package controllers

import (
	"fmt"
	"strings"

	errors2 "github.com/pkg/errors"

	"github.com/gofiber/fiber/v2"
	"github.com/penguin-statistics/backend-next/internal/models/dto"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
	"github.com/rs/zerolog/log"
)

type ReportController struct {
	crypto *utils.Crypto
}

func RegisterReportController(v2 *server.V2, v3 *server.V3, crypto *utils.Crypto) {
	c := &ReportController{
		crypto: crypto,
	}

	v2.Post("/report", c.SingularReport)
	v2.Post("/report/recognition", c.RecognitionReport)
	v2.Post("/intentionallypanic", func(ctx *fiber.Ctx) error {
		e := outer()

		fmt.Printf("#%+v#", e)

		log.Error().
			Stack().
			Err(e).
			Msg("trig")

		return e
	})
}

func inner() error {
	return errors2.New("seems we have an error here")
}

func middle() error {
	err := inner()
	if err != nil {
		return err
	}
	return nil
}

func outer() error {
	err := middle()
	if err != nil {
		return err
	}
	return nil
}

// @Summary      Submit an Item Drop Report
// @Description
// @Tags         Report
// @Produce      json
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/report [POST]
func (c *ReportController) SingularReport(ctx *fiber.Ctx) error {
	var report dto.SingularReportRequest
	if err := rekuest.ValidBody(ctx, &report); err != nil {
		return err
	}

	return ctx.JSON(report)
}

// @Summary      Bulk Submit with Frontend Recognition
// @Description  Submit an Item Drop Report with Frontend Recognition. Notice that this is a private API and is not designed for external use.
// @Tags         Report
// @Produce      json
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/report/recognition [POST]
func (c *ReportController) RecognitionReport(ctx *fiber.Ctx) error {
	encrypted := string(ctx.Body())

	segments := strings.SplitN(encrypted, ":", 2)

	if err := rekuest.Validate.Var(segments, "len=2"); err != nil {
		log.Warn().
			Err(err).
			Msg("Failed to decrypt recognition request")
		return errors.ErrInvalidRequest
	}

	privateKey := segments[0]
	body := segments[1]

	decrypted, err := c.crypto.Decrypt(privateKey, body)
	if err != nil {
		log.Warn().
			Err(err).
			Msg("Failed to decrypt recognition request")
		return errors.ErrInvalidRequest
	}

	return ctx.Send(decrypted)
}
