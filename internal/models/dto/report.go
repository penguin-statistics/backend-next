package dto

type SingularReportRequest struct {
	FragmentServer
	FragmentReportCommon

	Drops []Drop `json:"drops"`
}

type BatchReportRequest struct {
	FragmentServer
	FragmentReportCommon

	BatchDrops []BatchReportDrop `json:"batchDrops"`
	Timestamp  int64             `json:"timestamp"`
}

type BatchReportDrop struct {
	Drops    []Drop                `json:"drops"`
	StageID  string                `json:"stageId"`
	Metadata ReportRequestMetadata `json:"metadata"`
}

type ReportRequestMetadata struct {
	Fingerprint  string `json:"fingerprint"`
	Md5          string `json:"md5"`
	FileName     string `json:"fileName"`
	LastModified int64  `json:"lastModified"`
}

type Drop struct {
	DropType string `json:"dropType"`
	ItemID   string `json:"itemId"`
	Quantity int64  `json:"quantity"`
}
