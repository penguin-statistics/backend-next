package v3

import (
	"github.com/goccy/go-json"
	"gopkg.in/guregu/null.v3"
)

type Item struct {
	ItemID    int             `bun:",pk,autoincrement" json:"pgItemId"`
	ArkItemID string          `json:"arkItemId"`
	Name      json.RawMessage `json:"name" swaggertype:"object"`
	Existence json.RawMessage `json:"existence" swaggertype:"object"`
	SortID    int             `json:"sortId"`
	Rarity    int             `json:"rarity"`
	ItemType  string          `json:"type"`
	Group     null.String     `json:"group,omitempty" swaggertype:"string"`
	Sprite    null.String     `json:"sprite,omitempty" swaggertype:"string"`
	Keywords  json.RawMessage `json:"keywords,omitempty" swaggertype:"object"`
}
