package middlewares

import (
	"strings"

	ut "github.com/go-playground/universal-translator"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/text/language"

	"github.com/penguin-statistics/backend-next/internal/util/i18n"
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
			sanitized := strings.ReplaceAll(strings.ToLower(tag.String()), "-", "_")
			if sanitized == "zh_tw" {
				sanitized = "zh_hant"
			}
			langs = append(langs, sanitized)
		}

		trans, _ := i18n.UT.FindTranslator(langs...)

		return set(trans)
	}
}
