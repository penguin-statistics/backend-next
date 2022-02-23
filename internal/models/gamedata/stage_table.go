package gamedata

type StageTable struct {
	Stages map[string]*Stage `json:"stages"`
}

type Stage struct {
	StageID       string         `json:"stageId"`
	StageType     string         `json:"stageType"`
	ApCost        int            `json:"apCost"`
	Code          string         `json:"code"`
	ZoneID        string         `json:"zoneId"`
	StageDropInfo *StageDropInfo `json:"stageDropInfo"`
}

type StageDropInfo struct {
	DisplayDetailRewards []*DisplayDetailReward `json:"displayDetailRewards"`
}

type DisplayDetailReward struct {
	Id       string `json:"id"`
	DropType int    `json:"dropType"`
	Type     string `json:"type"`
}
