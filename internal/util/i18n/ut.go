package i18n

import (
	ut "github.com/go-playground/universal-translator"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/ja"
	"github.com/go-playground/locales/zh"
	"github.com/go-playground/locales/zh_Hant_TW"
)

var UT = ut.New(en.New(), zh_Hant_TW.New(), zh.New(), ja.New())
