package model

type AllTimesResultForGlobalDropMatrix struct {
	StageID int `json:"stageId" bun:"stage_id"`
	ItemID  int `json:"itemId" bun:"item_id"`
	Times   int `json:"times" bun:"times"`
}

type AllQuantitiesResultForGlobalDropMatrix struct {
	StageID  int `json:"stageId" bun:"stage_id"`
	ItemID   int `json:"itemId" bun:"item_id"`
	Quantity int `json:"quantity" bun:"quantity"`
}

type AllQuantityBucketsResultForGlobalDropMatrix struct {
	StageID         int         `json:"stageId" bun:"stage_id"`
	ItemID          int         `json:"itemId" bun:"item_id"`
	QuantityBuckets map[int]int `json:"quantityBuckets" bun:"type:jsonb"`
}
