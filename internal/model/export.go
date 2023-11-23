package model

type ExportDropReportsAndPatternsResult struct {
	DropReports  []*DropReportForExport  `json:"drop_reports"`
	DropPatterns []*DropPatternForExport `json:"drop_patterns"`
}

type DropReportForExport struct {
	Times      int    `json:"times"`
	PatternID  int    `json:"pattern_id"`
	CreatedAt  int64  `json:"created_at"`
	AccountID  int    `json:"account_id"`
	SourceName string `json:"source_name"`
	Version    string `json:"version"`
}

type DropPatternForExport struct {
	PatternID           int                            `json:"pattern_id"`
	DropPatternElements []*DropPatternElementForExport `json:"drop_pattern_elements"`
}

type DropPatternElementForExport struct {
	ArkItemID string `json:"itemId"`
	Quantity  int    `json:"quantity"`
}
