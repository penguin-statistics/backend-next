package service

import (
	"context"
	"testing"
	"time"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

// the shim stages, items and zones record a new last modified time when they are recalculated changed, and only then
func TestShimLastModifiedTime(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE zones (
		zone_id serial PRIMARY KEY,
		ark_zone_id character varying(255) NOT NULL,
		index integer NOT NULL DEFAULT 0,
		category text NOT NULL,
		type text,
		name jsonb,
		existence jsonb,
		background text
	)`)
	mustExec(t, db, `INSERT INTO zones (ark_zone_id, category, name, existence) VALUES ('main_1', 'MAINLINE', '{"en": "1"}', '{}')`)
	mustExec(t, db, `INSERT INTO stages (ark_stage_id, zone_id, stage_type, code, existence) VALUES ('main_01-07', 1, 'MAIN', '{"en": "1-7"}', '{}')`)
	mustExec(t, db, `INSERT INTO items (ark_item_id, type, name, existence) VALUES ('30012', 'MATERIAL', '{"en": "Orirock Cube"}', '{}')`)
	flushCaches(t)

	stage, item, zone := NewStage(repo.NewStage(db)), NewItem(repo.NewItem(db)), NewZone(repo.NewZone(db))
	tests := []struct {
		name string
		// key is the one the controller reads
		key         string
		recalculate func() error
		change      string
	}{
		{"stages", "[shimStages#server:CN]", func() error {
			if err := cache.ShimStages.Delete("CN"); err != nil {
				return err
			}
			_, err := stage.GetShimStages(ctx, "CN")
			return err
		}, `UPDATE stages SET code = '{"en": "1-8"}'`},
		{"items", "[shimItems]", func() error {
			if err := cache.ShimItems.Delete(); err != nil {
				return err
			}
			_, err := item.GetShimItems(ctx)
			return err
		}, `UPDATE items SET name = '{"en": "Orirock Cluster"}'`},
		{"zones", "[shimZones]", func() error {
			if err := cache.ShimZones.Delete(); err != nil {
				return err
			}
			_, err := zone.GetShimZones(ctx)
			return err
		}, `UPDATE zones SET name = '{"en": "2"}'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastModified := func() time.Time {
				t.Helper()
				if err := tt.recalculate(); err != nil {
					t.Fatal(err)
				}
				var lastModified time.Time
				if err := cache.LastModifiedTime.Get(tt.key, &lastModified); err != nil {
					t.Fatal(err)
				}
				return lastModified
			}

			first := lastModified()
			if got := lastModified(); !got.Equal(first) {
				t.Errorf("an unchanged recalculation changed the last modified time from %v to %v", first, got)
			}
			mustExec(t, db, tt.change)
			if got := lastModified(); !got.After(first) {
				t.Errorf("a changed recalculation recorded the last modified time %v, not after %v", got, first)
			}
		})
	}
}
