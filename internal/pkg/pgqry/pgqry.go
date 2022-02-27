package pgqry

import (
	"github.com/uptrace/bun"
)

type pq struct {
	Q *bun.SelectQuery
}

func New(bunQuery *bun.SelectQuery) *pq {
	return &pq{Q: bunQuery}
}

func (pq *pq) UseItemById(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN items AS it ON it.item_id = " + onColumn)
	return pq
}

func (pq *pq) UseStageById(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN stages AS st ON st.stage_id = " + onColumn)
	return pq
}

func (pq *pq) UseZoneById(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN zones AS zo ON zo.zone_id = " + onColumn)
	return pq
}

func (pq *pq) UseItemByArkId(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN items AS it ON it.ark_item_id = " + onColumn)
	return pq
}

func (pq *pq) UseStageByArkId(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN stages AS st ON st.ark_stage_id = " + onColumn)
	return pq
}

func (pq *pq) UseZoneByArkId(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN zones AS zo ON zo.ark_zone_id = " + onColumn)
	return pq
}

func (pq *pq) UseTimeRange(onColumn string) *pq {
	pq.Q = pq.Q.Join("LEFT JOIN time_ranges AS tr ON tr.range_id = " + onColumn)
	return pq
}

func (pq *pq) DoFilterCurrentTimeRange() *pq {
	pq.Q = pq.Q.Where("tr.start_time <= NOW() AND tr.end_time > NOW()")
	return pq
}
