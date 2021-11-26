package shims

import (
	"encoding/json"
	"penguin-stats-v4/internal/models/helpers"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PItem struct {
	bun.BaseModel `bun:"items"`

	ID           int64                `json:"-"`
	ArkItemID    string               `json:"itemId"`
	Name         string               `bun:"-" json:"name"`
	NameI18n     json.RawMessage      `bun:"name" json:"name_i18n"`
	Existence    json.RawMessage      `json:"existence"`
	SortID       int                  `json:"sortId"`
	Rarity       int                  `json:"rarity"`
	Group        null.String          `json:"groupID,omitempty"`
	Sprite       null.String          `json:"-"`
	SpriteCoords *helpers.NullIntSlice `bun:"-" json:"spriteCoords,omitempty"`
	Keywords     json.RawMessage      `json:"-"`
	AliasMap     json.RawMessage      `bun:"-" json:"alias,omitempty"`
	PronMap      json.RawMessage      `bun:"-" json:"pron,omitempty"`
}
