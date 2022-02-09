package shims

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type AccountController struct {
	service *service.AccountService
}

func RegisterAccountController(v2 *server.V2, s *service.AccountService) {
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
// @Success      200     {array}  shims.SiteStats
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/users [POST]
// @Deprecated
func (c *AccountController) Login(ctx *fiber.Ctx) error {
	inputPenguinId := ctx.Body()

	account, err := c.service.GetAccountByPenguinId(ctx, string(inputPenguinId))
	if err != nil {
		return err
	}

	// we even got emojis in PenguinID for some of the internal testers :)
	penguinId := url.QueryEscape(account.PenguinID)

	// Populate cookie
	ctx.Cookie(&fiber.Cookie{
		Name:     "userID",
		Value:    penguinId,
		MaxAge:   constants.PenguinIDAuthMaxCookieAgeSec,
		Path:     "/",
		Expires:  time.Now().Add(time.Second * constants.PenguinIDAuthMaxCookieAgeSec),
		Domain:   "." + ctx.Get("Origin"),
		SameSite: "None",
		Secure:   true,
	})

	// Sets the PenguinID in response header, used for scenarios
	// where cookie is not able to be used, such as in the Capacitor client.
	ctx.Set(constants.PenguinIDSetHeader, penguinId)

	// for some reasons the response for the login API is in format of
	// text/plain so I'd have to manually convert it to JSON and use ctx#Send to respond
	resp := shims.LoginResponse{
		UserID: account.PenguinID,
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	return ctx.Send(respBytes)
}
