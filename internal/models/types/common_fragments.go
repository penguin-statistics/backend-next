package types

type FragmentStageID struct {
	StageID string `validate:"required,printascii" required:"true" json:"stageId"`
}

type FragmentReportCommon struct {
	Server  string `validate:"required,caseinsensitiveoneof=CN US JP KR" required:"true" json:"server"`
	Source  string `validate:"required,printascii,max=32" required:"true" json:"source"`
	Version string `validate:"required,printascii,max=32" required:"true" json:"version"`
}
