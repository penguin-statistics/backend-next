package types

import "gopkg.in/guregu/null.v3"

type ArkDrop struct {
	DropType string `json:"dropType" validate:"required,oneof=REGULAR_DROP NORMAL_DROP SPECIAL_DROP EXTRA_DROP FURNITURE"`
	ItemID   string `json:"itemId" validate:"required,printascii" example:"30013"`
	Quantity int    `json:"quantity" validate:"required,lte=1000"`
}

type Drop struct {
	DropType string `json:"dropType"`
	ItemID   int    `json:"itemId"`
	Quantity int    `json:"quantity"`
}

type SingleReportRequest struct {
	FragmentStageID
	FragmentReportCommon

	Drops     []ArkDrop `json:"drops" validate:"dive"`
	PenguinID string    `json:"-"`

	Metadata *ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type SingleReportRecallRequest struct {
	ReportHash string `json:"reportHash" validate:"required,printascii" example:"0522ce0083000000-1wE2I9dvMFXXzBMpSCYM81rJ0T3tLrAQ"`
}

type BatchReportDrop struct {
	FragmentStageID

	Drops    []ArkDrop             `json:"drops" validate:"dive"`
	Metadata ReportRequestMetadata `json:"metadata" validate:"dive"`
}

type ReportRequestMetadata struct {
	Fingerprint  string      `json:"fingerprint,omitempty" validate:"lte=128"`
	MD5          null.String `json:"md5" validate:"lte=32" swaggertype:"string"`
	FileName     string      `json:"fileName" validate:"lte=512"`
	LastModified int         `json:"lastModified"`

	RecognizerVersion       null.String `json:"recognizerVersion" validate:"lte=32" swaggertype:"string"`
	RecognizerAssetsVersion null.String `json:"recognizerAssetsVersion" validate:"lte=32" swaggertype:"string"`
}

type BatchReportRequest struct {
	FragmentReportCommon

	BatchDrops []BatchReportDrop `json:"batchDrops" validate:"dive"`
}

type BatchReportError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason,omitempty"`
}
