package dto

type FragmentServer struct {
	Server string `validate:"required,caseinsensitiveoneof=CN US JP KR TW" required:"true" json:"server"`
}

type FragmentReportCommon struct {
	Source  string `validate:"required,max=32" required:"true" json:"source"`
	Version string `validate:"required,max=32" required:"true" json:"version"`
	StageID string `validate:"required" required:"true" json:"stageId"`
}
