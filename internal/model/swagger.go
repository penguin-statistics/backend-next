package model

// this file is only for providing swagger with correct types,
// and does not have any meaning on the real application logic.
// this file's declarations shall not be used in any of the application code.,
// with the only exception being the swagger documentation generator.

type I18nString struct {
	// ZH: 简体中文 (zh-CN)
	ZH string `json:"zh" validate:"required"`
	// ZH_HANT: 繁体中文 (zh-TW)
	ZH_HANT string `json:"zh-Hant" validate:"required"`
	// EN: English (en)
	EN string `json:"en" validate:"required"`
	// JP: 日本語 (ja)
	JA string `json:"ja" validate:"required"`
	// KR: 한국어 (ko)
	KO string `json:"ko" validate:"required"`
}

type I18nOptionalString struct {
	// ZH: 中文 (zh-CN)
	ZH string `json:"zh"`
	// ZH_HANT: 繁体中文 (zh-TW)
	ZH_HANT string `json:"zh-Hant"`
	// EN: English (en)
	EN string `json:"en"`
	// JP: 日本語 (ja)
	JA string `json:"ja"`
	// KR: 한국어 (ko)
	KO string `json:"ko"`
}

type ServerExistence struct {
	Exist     bool `json:"exist" validate:"required" example:"true"`
	StartTime *int `json:"openTime,omitempty" extension:"x-nullable" example:"1634799600000"`
	EndTime   *int `json:"closeTime,omitempty" extension:"x-nullable" example:"1635966000000"`
}

type Existence struct {
	// CN: 国服 Mainland China Server (maintained by Hypergryph Network Technology Co., Ltd.)
	CN ServerExistence `json:"CN" validate:"required"`
	// TW: 台服 Taiwan Server (maintained by 龍成網路有限公司)
	TW ServerExistence `json:"TW" validate:"required"`
	// US: 美服/国际服 Global Server (maintained by Yostar Limited)
	US ServerExistence `json:"US" validate:"required"`
	// JP: 日服 Japan Server (maintained by Yostar Inc,.)
	JP ServerExistence `json:"JP" validate:"required"`
	// KR: 韩服 Korea Server (maintained by Yostar Limited)
	KR ServerExistence `json:"KR" validate:"required"`
}

type Keywords struct {
	// Alias of the item
	Alias I18nOptionalString `json:"alias"`
	// Pronunciation hints of the item
	Pron I18nOptionalString `json:"pron"`
}
