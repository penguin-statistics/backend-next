package model

import (
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model/types"
)

type DropReportExtra struct {
	bun.BaseModel `bun:"drop_report_extras,alias:dre"`

	ReportID int                          `bun:",pk,autoincrement" json:"id"`
	IP       string                       `json:"ip"`
	Source   string                       `json:"source" bun:"source_name"`
	Version  string                       `json:"version"`
	Metadata *types.ReportRequestMetadata `json:"metadata"`
	MD5      null.String                  `json:"md5" swaggertype:"string"`
}
