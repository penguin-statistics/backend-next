package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Item struct {
	bun.BaseModel `bun:"items" swaggerignore:"true"`

	// ItemID (penguinItemId) is the numerical ID of the item.
	ItemID int `bun:",pk" json:"penguinItemId"`
	// ArkItemID (itemId) is the previously used, string form ID of the item; in JSON-representation `itemId` is used as key.
	ArkItemID string `json:"itemId"`
	// Name is a map with language code as key and the name of the item in that language as value.
	Name json.RawMessage `json:"name" swaggertype:"object"`
	// Existence is a map with server code as key and the existence of the item in that server as value.
	Existence json.RawMessage `json:"existence" swaggertype:"object"`
	// SortID is the sort position of the item.
	SortID int `json:"sortId"`
	Rarity int `json:"rarity"`
	// Group is an identifier of what the item actually is. For example, both orirock and orirock cube would have the same group, `orirock`.
	Group *null.String `json:"group,omitempty" swaggertype:"string"`
	// Sprite describes the location of the item's sprite on the sprite image, in a form of Y:X.
	Sprite *null.String `json:"sprite,omitempty" swaggertype:"string"`
	// Keywords is an arbitrary JSON object containing the keywords of the item, for optimizing the results of the frontend built-in search engine.
	Keywords json.RawMessage `json:"keywords,omitempty" swaggertype:"object"`
}
