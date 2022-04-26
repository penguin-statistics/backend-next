package rekuest

import (
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	enTranslations "github.com/go-playground/validator/v10/translations/en"
	jaTranslations "github.com/go-playground/validator/v10/translations/ja"
	zhTranslations "github.com/go-playground/validator/v10/translations/zh"
	zhTwTranslations "github.com/go-playground/validator/v10/translations/zh_tw"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/util"
	"github.com/penguin-statistics/backend-next/internal/util/i18n"
)

var Validate = util.NewValidator()

func init() {
	var err error
	entr, _ := i18n.UT.GetTranslator("en")
	err = enTranslations.RegisterDefaultTranslations(Validate, entr)
	if err != nil {
		log.Warn().Err(err).Str("locale", "en").Msg("could not register translation")
	}

	zhtr, _ := i18n.UT.GetTranslator("zh")
	err = zhTranslations.RegisterDefaultTranslations(Validate, zhtr)
	if err != nil {
		log.Warn().Err(err).Str("locale", "zh").Msg("could not register translation")
	}

	zhtwtr, _ := i18n.UT.GetTranslator("zh_Hant_TW")
	err = zhTwTranslations.RegisterDefaultTranslations(Validate, zhtwtr)
	if err != nil {
		log.Warn().Err(err).Str("locale", "zh_Hant_TW").Msg("could not register translation")
	}

	jatr, _ := i18n.UT.GetTranslator("ja")
	err = jaTranslations.RegisterDefaultTranslations(Validate, jatr)
	if err != nil {
		log.Warn().Err(err).Str("locale", "ja").Msg("could not register translation")
	}

	translators := map[string]ut.Translator{
		"en":         entr,
		"zh":         zhtr,
		"zh_Hant_TW": zhtwtr,
		"ja":         jatr,
	}

	for l, t := range translators {
		err = Validate.RegisterTranslation("caseinsensitiveoneof", t, func(ut ut.Translator) error {
			return nil
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("oneof", fe.Field(), fe.Param())
			return t
		})

		if err != nil {
			log.Warn().Err(err).Str("locale", l).Msg("could not register translation for function caseinsensitiveoneof")
		}
	}
}

type ErrorResponse struct {
	Field     string `json:"field,omitempty"`
	Violation string `json:"violation"`
	Message   string `json:"message"`
}

// Translate translates errors into ErrorResponses
func translate(utt ut.Translator, ve validator.ValidationErrors) []*ErrorResponse {
	trans := []*ErrorResponse{}

	var fe validator.FieldError

	for i := 0; i < len(ve); i++ {
		fe = ve[i]

		message := fe.Translate(utt)
		message = util.AddSpace(message)

		trans = append(trans, &ErrorResponse{
			Field:     fe.Namespace(),
			Violation: fe.Tag(),
			Message:   message,
		})
	}

	return trans
}

func validateVar(ctx *fiber.Ctx, s any, tag string) []*ErrorResponse {
	tr := TranslatorFromCtx(ctx)
	err := Validate.Var(s, tag)
	if err != nil {
		errs := err.(validator.ValidationErrors)
		return translate(tr, errs)
	}
	return nil
}

func validateStruct(ctx *fiber.Ctx, s any) []*ErrorResponse {
	tr := TranslatorFromCtx(ctx)
	err := Validate.Struct(s)
	if err != nil {
		errs, ok := err.(validator.ValidationErrors)
		if !ok {
			panic(err)
		}
		return translate(tr, errs)
	}
	return nil
}

// ValidBody will get the body from *fiber.Ctx using fiber#BodyParser(),
// and validate it using the validator singleton. If the validation passed it will write the unmarshalled body
// to dest and return a nil, otherwise it will return an error. Notice that dest shall
// always be a pointer.
func ValidBody(ctx *fiber.Ctx, dest any) error {
	if err := ctx.BodyParser(dest); err != nil {
		return pgerr.ErrInvalidReq.Msg("invalid request: %s", err)
	}

	if err := validateStruct(ctx, dest); err != nil {
		return pgerr.NewInvalidViolations(err)
	}

	return nil
}

func ValidStruct(ctx *fiber.Ctx, dest any) error {
	if err := validateStruct(ctx, dest); err != nil {
		return pgerr.NewInvalidViolations(err)
	}

	return nil
}

func ValidVar(ctx *fiber.Ctx, field any, tag string) error {
	if err := validateVar(ctx, field, tag); err != nil {
		return pgerr.NewInvalidViolations(err)
	}

	return nil
}

type request struct {
	Server string `validate:"required,alpha,oneof=CN US JP KR"`
}

func ValidServer(ctx *fiber.Ctx, server string) error {
	if err := ValidStruct(ctx, request{server}); err != nil {
		return err
	}

	return nil
}
