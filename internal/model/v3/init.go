package v3

type Init struct {
	Items  []*Item  `json:"items"`
	Stages []*Stage `json:"stages"`
	Zones  []*Zone  `json:"zones"`
}
