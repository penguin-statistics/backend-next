package mcp

import (
	"strings"
	"testing"

	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

// A drop rate has 4 significant digits, and at least 4 decimals: a rare drop is not 0.
func TestDropRate(t *testing.T) {
	for _, tc := range []struct {
		quantity, times int
		want            float64
	}{
		{3, 8537014, 3.514e-07},
		{1403, 19918, 0.07044},
		{2700151376, 2168783984, 1.245},
		{1977025, 1224363319, 0.001615},
		{48079, 32992, 1.4573},
		{0, 10, 0},
	} {
		row := service.SEORow{OneDropMatrixElement: &modelv2.OneDropMatrixElement{Times: tc.times, Quantity: tc.quantity}}
		if got := dropRate(row); got != tc.want {
			t.Errorf("dropRate(%d / %d) = %v, want %v", tc.quantity, tc.times, got, tc.want)
		}
	}
	row := service.SEORow{OneDropMatrixElement: &modelv2.OneDropMatrixElement{Times: 8537014, Quantity: 3}}
	if got := number(dropRate(row)); got != "0.0000003514" {
		t.Errorf("number(dropRate(3 / 8537014)) = %q", got)
	}
}

// A supply stage counts as open every day: the drop tools say that its rotation is not tracked.
func TestDropsWeekly(t *testing.T) {
	_, app := testServer(&fakeData{catalog: weeklyCatalog(), matrices: map[string]*service.SEOMatrix{
		"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
			{StageID: "wk_fly_5", ItemID: "3303", Times: 100000, Quantity: 150000, StartTime: 1556676000000},
		}, lastModified),
	}})
	if out, text := getStageDrops(t, app, `{"stage_id":"wk_fly_5"}`); !out.Stage.OpenNow || !strings.Contains(out.Note, rotationCaveat) ||
		!strings.Contains(text, rotationCaveat) {
		t.Errorf("stage drops %+v", out)
	}
	if out, text := getItemDropRates(t, app, `{"item_id":"3303"}`); len(out.Rows) != 1 || !strings.Contains(out.Note, rotationCaveat) ||
		!strings.Contains(text, rotationCaveat) {
		t.Errorf("item drop rates %+v", out)
	}
}
