package v2

type SiteStats struct {
	TotalStageTimes     []*TotalStageTime    `json:"totalStageTimes"`
	TotalStageTimes24H  []*TotalStageTime    `json:"totalStageTimes_24h"`
	TotalItemQuantities []*TotalItemQuantity `json:"totalItemQuantities"`
	TotalSanityCost     int                  `json:"totalApCost"`
}

type TotalItemQuantity struct {
	ItemID   string `json:"itemId" bun:"ark_item_id"`
	Quantity int    `json:"quantity" bun:"total_quantity"`
}

type TotalStageTime struct {
	StageID string `json:"stageId" bun:"ark_stage_id"`
	Times   int    `json:"times" bun:"total_times"`
}
