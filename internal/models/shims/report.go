package shims

type ReportResponse struct {
	ReportHash string `json:"reportHash" example:"0522ce0083000000-1wE2I9dvMFXXzBMpSCYM81rJ0T3tLrAQ"`
}

type RecognitionReportResponse struct {
	TaskId string   `json:"taskId" example:"0522ce0083000000-1wE2I9dvMFXXzBMpSCYM81rJ0T3tLrAQ"`
	Errors []string `json:"errors"`
}
