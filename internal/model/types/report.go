package types

// common report request structs
type ArkDrop struct {
	DropType string `json:"dropType" validate:"required,oneof=REGULAR_DROP NORMAL_DROP SPECIAL_DROP EXTRA_DROP FURNITURE"`
	ItemID   string `json:"itemId" validate:"required" example:"30013"`
	Quantity int    `json:"quantity" validate:"required,lte=1000"`
}

type Drop struct {
	DropType string `json:"dropType"`
	ItemID   int    `json:"itemId"`
	Quantity int    `json:"quantity"`
}

type ReportRequestMetadata struct {
	Fingerprint  string `json:"fingerprint,omitempty" validate:"lte=128"`
	MD5          string `json:"md5,omitempty" validate:"lte=32" swaggertype:"string"`
	FileName     string `json:"fileName,omitempty" validate:"lte=512"`
	LastModified int    `json:"lastModified,omitempty"`

	RecognizerVersion       string `json:"recognizerVersion,omitempty" validate:"omitempty,lte=32,semverprefixed" swaggertype:"string"`
	RecognizerAssetsVersion string `json:"recognizerAssetsVersion,omitempty" validate:"omitempty,lte=32,semverprefixed" swaggertype:"string"`
}

type SingularReportRequest struct {
	FragmentStageID
	FragmentReportCommon

	Drops []ArkDrop `json:"drops" validate:"dive"`

	Metadata *ReportRequestMetadata `json:"metadata" validate:"omitempty,dive"`
}

type BatchDrop struct {
	FragmentStageID

	Drops    []ArkDrop             `json:"drops" validate:"dive"`
	Metadata ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type BatchReportRequest struct {
	FragmentReportCommon

	BatchDrops []BatchDrop `json:"batchDrops" validate:"dive"`
}

type BatchReportError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason,omitempty"`
}

// report recall
type SingularReportRecallRequest struct {
	ReportHash string `json:"reportHash" validate:"required,printascii" example:"cahbuch1eqliv7dopen0-5ejlUrfzNMXNHY6Q"`
}
