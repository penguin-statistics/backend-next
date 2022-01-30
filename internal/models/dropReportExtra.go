package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
)

type DropReportExtra struct {
	bun.BaseModel `bun:"drop_report_extras,alias:dre"`

	ReportID int             `bun:",pk" json:"id"`
	IP       string          `json:"ip"`
	Source   string          `json:"source" bun:"source_name"`
	Version  string          `json:"version"`
	Metadata json.RawMessage `json:"metadata"`
	MD5      string          `json:"md5"`
}
