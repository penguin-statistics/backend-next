package model

import (
	"time"

	"github.com/uptrace/bun"
)

type Snapshot struct {
	bun.BaseModel `bun:"snapshots"`

	SnapshotID int        `bun:",pk,autoincrement" json:"id"`
	CreatedAt  *time.Time `bun:"created_at" json:"created_at"`
	Key        string     `bun:"key" json:"key"`
	Version    string     `bun:"version" json:"version"`
	Content    string     `bun:"content" json:"content"`
}
