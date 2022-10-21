package middlewares

import (
	"strings"

	ut "github.com/go-playground/universal-translator"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/text/language"

	"exusiai.dev/backend-next/internal/util/i18n"
)

func InjectI18n() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		set := func(trans ut.Translator) error {
			c.Locals("T", trans)
			return c.Next()
		}

		tags, _, err := language.ParseAcceptLanguage(c.Get(fiber.HeaderAcceptLanguage))
		if err != nil {
			return set(i18n.UT.GetFallback())
		}

		var langs []string

		for _, tag := range tags {
			langs = append(langs, strings.ReplaceAll(strings.ToLower(tag.String()), "-", "_"))
		}

		trans, _ := i18n.UT.FindTranslator(langs...)

		return set(trans)
	}
}
