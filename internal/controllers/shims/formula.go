package shims

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server"
)

type FormulaController struct {
}

func RegisterFormulaController(v2 *server.V2) {
	c := &FormulaController{}

	v2.Get("/formula", c.GetFormula)
}

// @Summary      Get Formula
// @Tags         Formula
// @Produce      json
// @Success      200     {array}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/formula [GET]
// @Deprecated
func (c *FormulaController) GetFormula(ctx *fiber.Ctx) error {
	var formula interface{}
	err := cache.Formula.Get("formula", &formula)
	if err == nil {
		return ctx.JSON(formula)
	}

	res, err := http.Get(constants.FormulaFilePath)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get formula")
	}

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}

	json.Unmarshal([]byte(body), &formula)
	cache.Formula.Set("", formula, 24*time.Hour)

	return ctx.JSON(formula)
}
