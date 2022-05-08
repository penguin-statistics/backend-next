package types

type ReportTaskSingleReport struct {
	FragmentStageID

	Drops []*Drop `json:"drops" validate:"dive"`
	Times int     `json:"times"`

	// Metadata is optional
	Metadata *ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportTask struct {
	TaskID string `json:"taskId"`
	// CreatedAt is the time the task was created, in microseconds since the epoch.
	CreatedAt int64 `json:"createdAt"`
	FragmentReportCommon

	Reports []*ReportTaskSingleReport `json:"report"`

	AccountID int    `json:"accountId"`
	IP        string `json:"ip"`
}
