package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PItem struct {
	bun.BaseModel `bun:"items"`

	ID        int64           `json:"id"`
	ArkItemID string          `json:"arkItemId"`
	Name      json.RawMessage `json:"name"`
	Existence json.RawMessage `json:"existence"`
	SortID    int             `json:"sortId"`
	Rarity    int             `json:"rarity"`
	Group     null.String     `json:"group"`
	Sprite    null.String     `json:"sprite"`
	Keywords  json.RawMessage `json:"keywords"`
}
