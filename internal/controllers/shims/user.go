package shims

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type AccountController struct {
	service *service.AccountService
}

func RegisterAccountController(v2 *svr.V2, s *service.AccountService) {
	c := &AccountController{
		service: s,
	}
	v2.Post("/users", c.Login)
}

// @Summary      Login with PenguinID
// @Tags         Account
// @Accept       plain
// @Produce      plain
// @Param        userId  body      int  true  "User ID"
// @Success      200     {object}   shims.LoginResponse  "User ID. In the deprecated backend this is, for some reason, been implemented to return a JSON in the response body but with a `Content-Type: text/plain` in the response header instead of the correct `Content-Type: application/json`. So the v2 API has replicated this behavior to ensure compatibility."
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Security     PenguinIDAuth
// @Router       /PenguinStats/api/v2/users [POST]
func (c *AccountController) Login(ctx *fiber.Ctx) error {
	inputPenguinId := ctx.Body()

	account, err := c.service.GetAccountByPenguinId(ctx.Context(), string(inputPenguinId))
	if err != nil {
		return err
	}

	pgid.Inject(ctx, account.PenguinID)

	// for some reasons the response for the login API is in format of
	// text/plain so I'd have to manually convert it to JSON and use ctx#Send to respond
	// to ensure compatibility
	resp, err := json.Marshal(&shims.LoginResponse{
		UserID: account.PenguinID,
	})
	if err != nil {
		return err
	}

	cachectrl.OptOut(ctx)

	return ctx.Send(resp)
}
