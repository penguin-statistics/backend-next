package types

type ArkDrop struct {
	DropType string `json:"dropType" validate:"required,oneof=REGULAR_DROP NORMAL_DROP SPECIAL_DROP EXTRA_DROP"`
	ItemID   string `json:"itemId" validate:"required"`
	Quantity int    `json:"quantity" validate:"required,lte=1000"`
}

type SingleReportRequest struct {
	FragmentStageID
	FragmentReportCommon

	Drops []ArkDrop `json:"drops" validate:"dive"`
}

type BatchReportDrop struct {
	Drops    []ArkDrop             `json:"drops"`
	StageID  string                `json:"stageId"`
	Metadata ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportRequestMetadata struct {
	Fingerprint  string `json:"fingerprint" validate:"lte=128"`
	MD5          string `json:"md5" validate:"lte=32"`
	FileName     string `json:"fileName" validate:"lte=512"`
	LastModified int    `json:"lastModified"`
}

type BatchReportRequest struct {
	FragmentStageID
	FragmentReportCommon

	BatchDrops []BatchReportDrop `json:"batchDrops"`
}
