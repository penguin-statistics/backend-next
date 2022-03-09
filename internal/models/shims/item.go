package shims

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Item struct {
	bun.BaseModel `bun:"items"`

	ItemID      int             `bun:",pk" json:"-"`
	ArkItemID   string          `json:"itemId"`
	Name        string          `bun:"-" json:"name"`
	NameI18n    json.RawMessage `bun:"name" json:"name_i18n" swaggertype:"object"`
	Existence   json.RawMessage `json:"existence" swaggertype:"object"`
	ItemType    string          `bun:"column:type" json:"itemType"`
	SortID      int             `json:"sortId"`
	Rarity      int             `json:"rarity"`
	Group       null.String     `json:"groupID,omitempty" swaggertype:"string"`
	Sprite      null.String     `json:"-" swaggertype:"string"`
	SpriteCoord *[]int          `bun:"-" json:"spriteCoord,omitempty"`
	Keywords    json.RawMessage `json:"-" swaggertype:"object"`
	AliasMap    json.RawMessage `bun:"-" json:"alias,omitempty" swaggertype:"array,string"`
	PronMap     json.RawMessage `bun:"-" json:"pron,omitempty" swaggertype:"array,string"`
}
