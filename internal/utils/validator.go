package utils

import (
	"strings"

	"github.com/go-playground/validator/v10"
)

func NewValidator() *validator.Validate {
	validate := validator.New()
	validate.RegisterValidation("caseinsensitiveoneof", caseInsensitiveOneOf)

	return validate
}

func caseInsensitiveOneOf(fl validator.FieldLevel) bool {
	val := strings.ToLower(fl.Field().String())
	candidates := strings.Split(strings.ToLower(fl.Param()), " ")
	for _, v := range candidates {
		if val == v {
			return true
		}
	}
	return false
}
