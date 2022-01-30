package types

type SingleReport struct {
	FragmentStageID

	Drops []Drop `json:"drops" validate:"dive"`

	// Metadata is optional; if not provided, the report will be treated as a single report.
	Metadata *ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportContext struct {
	FragmentReportCommon

	Reports []*SingleReport `json:"report"`

	PenguinID string `json:"penguinId"`
	IP        string `json:"ip"`
}
