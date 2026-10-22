package model

import (
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model/types"
)

type DropReportExtra struct {
	bun.BaseModel `bun:"drop_report_extras,alias:dre"`

	ReportID int                          `bun:",pk,autoincrement" json:"id"`
	IP       string                       `json:"ip"`
	Metadata *types.ReportRequestMetadata `json:"metadata"`
	MD5      null.String                  `json:"md5" swaggertype:"string"`

	// CreatedAt is the created_at of the report, the partition key of drop_report_extras on the new database of #752
	// (scripts/newdb). Today's database has no such column, so bun neither selects nor inserts it: CreateDropReportExtra
	// writes it when it is set, which the report worker does only with appconfig.ConfigSpec.ReportSchemaV2Enabled.
	// Reads leave it nil.
	CreatedAt *time.Time `bun:"-" json:"-"`
}
