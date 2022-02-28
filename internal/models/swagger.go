package models

// this file is mainly for providing swagger with correct types

type I18nString struct {
	// ZH: 中文 (zh-CN)
	ZH string `json:"zh" validate:"required"`
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
	// EN: English (en)
	EN string `json:"en"`
	// JP: 日本語 (ja)
	JA string `json:"ja"`
	// KR: 한국어 (ko)
	KO string `json:"ko"`
}

type ServerExistence struct {
	Exist     bool `json:"exist" validate:"required"`
	StartTime *int `json:"startTime,omitempty"`
	EndTime   *int `json:"endTime,omitempty"`
}

type Existence struct {
	// CN: 国服 Mainland China Server (maintained by Hypergryph Network Technology Co., Ltd.)
	CN ServerExistence `json:"CN" validate:"required"`
	// US: 美服/国际服 Global Server (maintained by Yostar Limited)
	US ServerExistence `json:"US" validate:"required"`
	// JP: 日服 Japan Server (maintained by Yostar Inc,.)
	JP ServerExistence `json:"JP" validate:"required"`
	// KR: 韩服 Korea Server (maintained by Yostar Limited)
	KR ServerExistence `json:"KR" validate:"required"`
	// // TW: 台服 Taiwan Server (maintained by 龍成網路有限公司)
	// TW ServerExistence `json:"TW" validate:"required"`
}

type Keywords struct {
	// Alias of the item,
	Alias I18nOptionalString `json:"alias"`
	// Pronunciation hints of the item
	Pron I18nOptionalString `json:"pron"`
}
