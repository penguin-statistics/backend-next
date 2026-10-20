package service

import (
	"context"
	"testing"
	"time"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
)

// TestGetShimStageByArkIdServesTheRequestedServer seeds stage main_01-07 dropping item_cn on CN and item_us on US,
// both open now, and looks it up as /v2/stages/:stageId?server= does.
func TestGetShimStageByArkIdServesTheRequestedServer(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustExec(t, db, "CREATE TABLE zones (zone_id serial PRIMARY KEY, ark_zone_id character varying(255) NOT NULL)")
	mustExec(t, db, "INSERT INTO zones (zone_id, ark_zone_id) VALUES (1, 'main_1')")
	mustExec(t, db, `INSERT INTO stages (stage_id, ark_stage_id, zone_id, stage_type, code, sanity) VALUES (1, 'main_01-07', 1, 'MAIN', '{"zh": "1-7"}', 6)`)
	mustExec(t, db, "INSERT INTO items (item_id, ark_item_id, type) VALUES (10, 'item_cn', 'MATERIAL'), (11, 'item_us', 'MATERIAL')")
	now := time.Now()
	for _, server := range []string{"CN", "US"} {
		var rangeId int64
		if err := db.NewRaw("INSERT INTO time_ranges (start_time, end_time, server) VALUES (?, ?, ?) RETURNING range_id",
			now.Add(-time.Hour), now.Add(time.Hour), server).Scan(ctx, &rangeId); err != nil {
			t.Fatal(err)
		}
		itemId := map[string]int{"CN": 10, "US": 11}[server]
		mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, 1, ?, 'REGULAR', ?, true)",
			server, itemId, rangeId)
	}
	flushCaches(t)
	s := NewStage(repo.NewStage(db))

	// CN first, so that a lookup cached by stage id alone would serve CN's drop infos to US
	for _, server := range []string{"CN", "US", "CN"} {
		stage, err := s.GetShimStageByArkId(ctx, "main_01-07", server)
		if err != nil {
			t.Fatal(err)
		}
		itemIds := make([]string, 0, len(stage.DropInfos))
		for _, dropInfo := range stage.DropInfos {
			itemIds = append(itemIds, dropInfo.ArkItemID)
		}
		want := map[string]string{"CN": "item_cn", "US": "item_us"}[server]
		if len(itemIds) != 1 || itemIds[0] != want {
			t.Fatalf("%s: drop infos for %v, want only %s", server, itemIds, want)
		}
		if stage.ArkZoneID != "main_1" || stage.Code != "1-7" {
			t.Fatalf("%s: zone %q code %q, want the shim fields applied", server, stage.ArkZoneID, stage.Code)
		}
	}

	if _, err := s.GetShimStageByArkId(ctx, "main_99-99", "CN"); err != pgerr.ErrNotFound {
		t.Fatalf("unknown stage: err %v, want pgerr.ErrNotFound", err)
	}
}
