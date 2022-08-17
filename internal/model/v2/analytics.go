package v2

type UniqueUserCountBySource struct {
	SourceName string `json:"source_name" bun:"source_name"`
	Count      int    `json:"count" bun:"count"`
}
