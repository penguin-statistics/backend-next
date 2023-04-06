package util

import (
	"reflect"
	"regexp"
	"strings"

	"exusiai.dev/gommon/constant"
	"github.com/go-playground/validator/v10"
	"gopkg.in/guregu/null.v3"
)

var (
	// from https://github.com/go-playground/validator/blob/9e2ea4038020b5c7e3802a21cfa4e3afcfdcd276/regexes.go
	semverRegexString = `^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$` // numbered capture groups https://semver.org/
	semverRegex       = regexp.MustCompile(semverRegexString)
)

func NewValidator() *validator.Validate {
	validate := validator.New()
	validate.RegisterValidation("caseinsensitiveoneof", caseInsensitiveOneOf)
	validate.RegisterValidation("semverprefixed", semverPrefixed)
	validate.RegisterValidation("arkserver", arkServer)
	validate.RegisterValidation("sourcecategory", sourceCategory)
	validate.RegisterCustomTypeFunc(nullIntValuer, null.Int{})
	validate.RegisterCustomTypeFunc(nullStringValuer, null.String{})

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

func semverPrefixed(fl validator.FieldLevel) bool {
	val := fl.Field().String()
	trimmed := strings.TrimPrefix(val, "v")
	return semverRegex.MatchString(trimmed)
}

func arkServer(fl validator.FieldLevel) bool {
	val := fl.Field().String()
	_, ok := constant.ServerMap[val]
	return ok
}

func sourceCategory(fl validator.FieldLevel) bool {
	val := fl.Field().String()
	return val == "" || val == constant.SourceCategoryAll || val == constant.SourceCategoryAutomated || val == constant.SourceCategoryManual
}

func nullIntValuer(field reflect.Value) interface{} {
	if valuer, ok := field.Interface().(null.Int); ok {
		return valuer.Int64
	}

	return nil
}

func nullStringValuer(field reflect.Value) interface{} {
	if valuer, ok := field.Interface().(null.String); ok {
		return valuer.String
	}

	return nil
}
