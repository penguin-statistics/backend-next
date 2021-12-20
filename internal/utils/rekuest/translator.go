package rekuest

import (
	ut "github.com/go-playground/universal-translator"
	"github.com/gofiber/fiber/v2"
)

func TranslatorFromCtx(ctx *fiber.Ctx) ut.Translator {
	return ctx.Locals("T").(ut.Translator)
}
