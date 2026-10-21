package service

// Integration tests of the item lookups by ark id on PostgreSQL. They are skipped unless PENGUIN_TEST_POSTGRES_DSN
// points at a disposable database, see integration_test.go.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util/reportutil"
)

type itemQueryCounter struct {
	n atomic.Int64
}

func (c *itemQueryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (c *itemQueryCounter) AfterQuery(context.Context, *bun.QueryEvent) {
	c.n.Add(1)
}

func newItemTestDB(t *testing.T) (*bun.DB, *itemQueryCounter) {
	t.Helper()
	db := newTestDB(t)
	for _, arkItemId := range []string{"30013", "30012", "furni_1"} {
		mustExec(t, db, "INSERT INTO items (ark_item_id, type) VALUES (?, 'MATERIAL')", arkItemId)
	}
	flushCaches(t)
	counter := &itemQueryCounter{}
	db.AddQueryHook(counter)
	return db, counter
}

// legacyMergeDropsAndMapDropTypes is PipelineMergeDropsAndMapDropTypes as it was before the item ids missing from
// itemsMapByArkId were no longer queried: every drop's item is queried, and the unknown ones are skipped.
func legacyMergeDropsAndMapDropTypes(ctx context.Context, itemRepo *repo.Item, drops []types.ArkDrop) ([]*types.Drop, error) {
	convertedDrops := make([]*types.Drop, 0, len(drops))
	for _, drop := range drops {
		item, err := itemRepo.GetItemByArkId(ctx, drop.ItemID)
		if err != nil {
			if !errors.Is(err, pgerr.ErrNotFound) {
				return nil, err
			}
			continue
		}
		convertedDrops = append(convertedDrops, &types.Drop{
			DropType: constant.DropTypeMap[drop.DropType],
			ItemID:   item.ItemID,
			Quantity: drop.Quantity,
		})
	}
	return reportutil.MergeDropsByDropTypeAndItemID(convertedDrops), nil
}

func dropLines(drops []*types.Drop) string {
	lines := make([]string, 0, len(drops))
	for _, drop := range drops {
		lines = append(lines, fmt.Sprintf("%s|%d|%d", drop.DropType, drop.ItemID, drop.Quantity))
	}
	// MergeDropsByDropTypeAndItemID groups through a map, so the order of its drops varies from run to run
	sort.Strings(lines)
	return strings.Join(lines, ",")
}

func TestPipelineMergeDropsAndMapDropTypesUnknownItems(t *testing.T) {
	db, counter := newItemTestDB(t)
	ctx := context.Background()
	itemRepo := repo.NewItem(db)
	s := &Report{ItemService: NewItem(itemRepo)}

	cases := []struct {
		name  string
		drops []types.ArkDrop
		// queries is the number of queries the conversion makes, run in order on the caches the earlier cases left
		queries int64
	}{
		{"empty", nil, 0},
		{"known and unknown", []types.ArkDrop{
			{DropType: "NORMAL_DROP", ItemID: "30013", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "unknown_1", Quantity: 3},
			{DropType: "EXTRA_DROP", ItemID: "30012", Quantity: 2},
			{DropType: "REGULAR_DROP", ItemID: "30013", Quantity: 2},
			{DropType: "SPECIAL_DROP", ItemID: "unknown_2", Quantity: 1},
			{DropType: "FURNITURE", ItemID: "unknown_1", Quantity: 1},
			{DropType: "FURNITURE", ItemID: "furni_1", Quantity: 1},
		}, 4}, // itemsMapByArkId, then 30013, 30012 and furni_1
		{"known and unknown again", []types.ArkDrop{
			{DropType: "NORMAL_DROP", ItemID: "unknown_1", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "30012", Quantity: 1},
			{DropType: "SPECIAL_DROP", ItemID: "unknown_2", Quantity: 4},
		}, 0},
		{"unknown only, never seen", []types.ArkDrop{
			{DropType: "NORMAL_DROP", ItemID: "unknown_3", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "30013 ", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "", Quantity: 1},
		}, 0},
		// bun drops NUL characters from the id it sends, so that 30013\x00 is 30013
		{"ids bun alters", []types.ArkDrop{
			{DropType: "NORMAL_DROP", ItemID: "30013\x00", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "300\x0012", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "unknown\x00_1", Quantity: 1},
			{DropType: "NORMAL_DROP", ItemID: "30013\xff", Quantity: 1},
		}, 4},
	}
	for _, c := range cases {
		want, err := legacyMergeDropsAndMapDropTypes(ctx, itemRepo, c.drops)
		if err != nil {
			t.Fatal(err)
		}
		before := counter.n.Load()
		got, err := s.PipelineMergeDropsAndMapDropTypes(ctx, c.drops)
		if err != nil {
			t.Fatal(err)
		}
		if queries := counter.n.Load() - before; queries != c.queries {
			t.Errorf("%s: %d queries, want %d", c.name, queries, c.queries)
		}
		if dropLines(got) != dropLines(want) {
			t.Errorf("%s: drops %q, want %q", c.name, dropLines(got), dropLines(want))
		}
	}
}

func TestGetItemByArkIdUnknown(t *testing.T) {
	db, counter := newItemTestDB(t)
	ctx := context.Background()
	s := NewItem(repo.NewItem(db))

	// a failing load of itemsMapByArkId is returned, not taken for an unknown item, nor cached
	mustExec(t, db, "ALTER TABLE items RENAME TO items_away")
	if _, err := s.GetItemByArkId(ctx, "30013"); err == nil || errors.Is(err, pgerr.ErrNotFound) {
		t.Fatalf("got error %v, want the failed query", err)
	}
	mustExec(t, db, "ALTER TABLE items_away RENAME TO items")

	before := counter.n.Load()
	for i := 0; i < 3; i++ {
		item, err := s.GetItemByArkId(ctx, "30013")
		if err != nil {
			t.Fatal(err)
		}
		if item.ArkItemID != "30013" {
			t.Fatalf("got item %q, want 30013", item.ArkItemID)
		}
		if _, err := s.GetItemByArkId(ctx, "unknown_1"); !errors.Is(err, pgerr.ErrNotFound) {
			t.Fatalf("got error %v, want pgerr.ErrNotFound", err)
		}
	}
	if queries := counter.n.Load() - before; queries != 2 {
		t.Errorf("%d queries, want 2: itemsMapByArkId and 30013", queries)
	}

	// an item inserted is found once itemsMapByArkId expires, although the items cached before it lack the item
	if _, err := s.GetItems(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, "INSERT INTO items (ark_item_id, type) VALUES ('unknown_1', 'MATERIAL')")
	if err := cache.ItemsMapByArkID.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItemByArkId(ctx, "unknown_1"); err != nil {
		t.Fatalf("got error %v, want the item inserted", err)
	}
}
