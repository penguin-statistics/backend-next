package v2

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

type Recognition struct {
	fx.In

	RecognitionDefectRepo *repo.RecognitionDefect
	AccountService        *service.Account
	UpyunService          *service.Upyun
}

func RegisterUpyun(v2 *svr.V2, c Recognition) {
	r := v2.Group("/recognition")
	r.Post("/defects/report/init", c.InitDefectReport)
	r.Post("/defects/report/callback/:defectId", c.RetrieveDefectReportImageCallback)
}

type (
	InitDefectReportRequestEnvironment struct {
		FrontendVersion         string `json:"frontendVersion" validate:"required,max=32,printascii"`
		FrontendCommit          string `json:"frontendCommit" validate:"required,max=32,printascii"`
		RecognizerVersion       string `json:"recognizerVersion" validate:"required,max=32,printascii"`
		RecognizerOpenCVVersion string `json:"recognizerOpenCVVersion" validate:"required,max=32,printascii"`
		RecognizerAssetsVersion string `json:"recognizerAssetsVersion" validate:"required,max=32,printascii"`
		Server                  string `json:"server" validate:"required,arkserver"`
		SessionID               string `json:"sessionId" validate:"required,len=8,alphanum"`
	}
	InitDefectReportRequest struct {
		RecognitionResult json.RawMessage                    `json:"recognitionResult" validate:"dive"`
		Environment       InitDefectReportRequestEnvironment `json:"environment" validate:"required,dive"`
	}
	InitDefectReportResponseUploadParams struct {
		URL           string `json:"url"`
		Authorization string `json:"authorization"`
		Policy        string `json:"policy"`
	}
	InitDefectReportResponse struct {
		UploadParams InitDefectReportResponseUploadParams `json:"uploadParams"`
		DefectID     string                               `json:"defectId"`
	}
)

func (c *Recognition) InitDefectReport(ctx *fiber.Ctx) error {
	var req InitDefectReportRequest
	if err := rekuest.ValidBody(ctx, &req); err != nil {
		return err
	}

	var accountId int
	account, _ := c.AccountService.GetAccountFromRequest(ctx)
	if account != nil {
		accountId = account.AccountID
	}

	environment, err := json.Marshal(req.Environment)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal environment")
		return err
	}

	defect := model.RecognitionDefect{
		SessionID:         req.Environment.SessionID,
		AccountID:         accountId,
		RecognitionResult: req.RecognitionResult,
		Environment:       json.RawMessage(environment),
	}

	err = c.RecognitionDefectRepo.CreateDefectReportDraft(ctx.Context(), &defect)
	if err != nil {
		log.Error().Err(err).Msg("failed to create defect report draft")
		return err
	}

	upyunImageInitResponse, err := c.UpyunService.InitImageUpload("recognition/defects/images", defect.DefectID)
	if err != nil {
		log.Error().Err(err).Msg("failed to init image upload")
		return pgerr.ErrInternalError.Msg("failed to init image upload")
	}

	return ctx.JSON(InitDefectReportResponse{
		DefectID:     defect.DefectID,
		UploadParams: InitDefectReportResponseUploadParams(upyunImageInitResponse),
	})
}

func (c *Recognition) RetrieveDefectReportImageCallback(ctx *fiber.Ctx) error {
	defectId := ctx.Params("defectId")
	if defectId == "" {
		return pgerr.ErrInvalidReq.Msg("defectId is required")
	}

	path, err := c.UpyunService.VerifyImageUploadCallback(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to verify image upload callback")
		return pgerr.ErrInvalidReq.Msg("failed to verify image upload callback")
	}

	err = c.RecognitionDefectRepo.FinalizeDefectReport(ctx.Context(), defectId, c.UpyunService.MarshalImageURI(path))
	if err != nil {
		log.Error().Err(err).Msg("failed to finalize defect report")
		return pgerr.ErrInternalError.Msg("failed to finalize defect report")
	}

	return nil
}
