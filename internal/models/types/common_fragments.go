package types

type FragmentStageID struct {
	StageID string `validate:"required,printascii" required:"true" json:"stageId" example:"main_01-07"`
}

type FragmentReportCommon struct {
	Server string `validate:"required,alpha,caseinsensitiveoneof=CN US JP KR" required:"true" json:"server" example:"CN"`
	// Source describes a source of the report. Third-party API consumers should change this to their own name.
	Source string `validate:"required,printascii,max=32" required:"true" json:"source" example:"frontend-v2"`
	// Version describes the version of the source app used to submit this report. Third-party API consumers should change this to their own app version.
	Version string `validate:"required,printascii,max=32" required:"true" json:"version" example:"v0.0.0+0000000"`
}
