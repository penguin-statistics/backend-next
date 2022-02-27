package shims

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type FormulaController struct {
	FormulaService *service.FormulaService
}

func RegisterFormulaController(v2 *server.V2, formulaService *service.FormulaService) {
	c := &FormulaController{
		FormulaService: formulaService,
	}

	v2.Get("/formula", c.GetFormula)
}

// @Summary      Get Formula
// @Tags         Formula
// @Produce      json
// @Success      200
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/formula [GET]
// @Deprecated
func (c *FormulaController) GetFormula(ctx *fiber.Ctx) error {
	formula, err := c.FormulaService.GetFormula(ctx.Context())
	if err != nil {
		return err
	}
	return ctx.JSON(formula)
}
