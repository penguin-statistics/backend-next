package dto

type Drop struct {
	DropType string `json:"dropType" validate:"required,oneof=REGULAR SPECIAL EXTRA NORMAL_DROP SPECIAL_DROP EXTRA_DROP"`
	ItemID   string `json:"itemId" validate:"required"`
	Quantity int    `json:"quantity" validate:"required,max:1000"`
}

type SingularReportRequest struct {
	FragmentServer
	FragmentReportCommon

	Drops []Drop `json:"drops" validate:"max:64"`
}

type BatchReportRequest struct {
	FragmentServer
	FragmentReportCommon

	BatchDrops []BatchReportDrop `json:"batchDrops"`
	Timestamp  int               `json:"timestamp"`
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
	LastModified int    `json:"lastModified"`
}
