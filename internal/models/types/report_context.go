package types

type SingleReport struct {
	FragmentStageID

	Drops []ArkDrop `json:"drops" validate:"dive"`
	Times int       `json:"times"`

	// Metadata is optional; if not provided, the report will be treated as a single report.
	Metadata *ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportTask struct {
	FragmentReportCommon

	Reports []*SingleReport `json:"report"`

	AccountID int    `json:"accountId"`
	IP        string `json:"ip"`
}
