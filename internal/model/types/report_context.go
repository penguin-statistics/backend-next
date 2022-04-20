package types

type SingleReport struct {
	FragmentStageID

	Drops []*Drop `json:"drops" validate:"dive"`
	Times int     `json:"times"`

	// Metadata is optional; if not provided, the report will be treated as a single report.
	Metadata *ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportTask struct {
	TaskID string `json:"taskId"`
	// CreatedAt is the time the task was created, in microseconds since the epoch.
	CreatedAt int64 `json:"createdAt"`
	FragmentReportCommon

	Reports []*SingleReport `json:"report"`

	AccountID int    `json:"accountId"`
	IP        string `json:"ip"`
}
