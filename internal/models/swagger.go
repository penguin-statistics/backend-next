package models

// this file is mainly for providing swagger with correct types

type I18nString struct {
	ZH string `json:"zh" validate:"required"`
	EN string `json:"en" validate:"required"`
	JA string `json:"ja" validate:"required"`
	KO string `json:"ko" validate:"required"`
}

type I18nOptionalString struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
	JA string `json:"ja"`
	KO string `json:"ko"`
}

type ServerExistence struct {
	Exist     bool `json:"exist" validate:"required"`
	StartTime *int `json:"startTime,omitempty"`
	EndTime   *int `json:"endTime,omitempty"`
}

type Existence struct {
	CN ServerExistence `json:"CN" validate:"required"`
	US ServerExistence `json:"US" validate:"required"`
	JP ServerExistence `json:"JP" validate:"required"`
	KR ServerExistence `json:"KR" validate:"required"`
}

type Keywords struct {
	Alias I18nOptionalString `json:"alias" validate:"required"`
	Pron  I18nOptionalString `json:"pron" validate:"required"`
}
