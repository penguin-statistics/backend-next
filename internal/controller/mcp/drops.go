package mcp

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/backend-next/internal/service"
)

// What the drop tools (get_item_drop_rates and get_stage_drops) share: the numbers of a row of the matrix, and the
// item or stage an argument names.

// minSamples is the fewest runs of a row whose rate is not noise: a row of fewer is flagged lowSample, and ranks after
// the others by sanity per item. On CN in October 2026, the best sanity per item of Damaged Device was that of 4 runs,
// the next one that of 3975.
const minSamples = 100

// dropRate is the drop rate of a row, quantity ÷ times to 4 significant digits but at least 4 decimals, so that a rare
// drop is not 0 (3 in 8537014 runs is 0.0000003514). It divides the counts: the rate of the row is rounded already.
// The row has runs (see counted).
func dropRate(row service.SEORow) float64 {
	x := float64(row.Quantity) / float64(row.Times)
	if x == 0 {
		return 0
	}
	p := math.Pow10(max(4, 3-int(math.Floor(math.Log10(x)))))
	return math.Round(x*p) / p
}

// counted tells whether the row has runs, and so a rate: the matrix has no row without, but one would make the rate
// NaN or infinite, which JSON cannot carry.
func counted(row service.SEORow) bool {
	return row.Times > 0
}

// sanityPerItem is the sanity per item of a row of the stage rounded to 3 decimals, nil (null) if it has none (see
// service.SEOStage.SanityPerItem); the stage may be nil, if the catalog does not have it.
func sanityPerItem(stage *service.SEOStage, row service.SEORow) *float64 {
	perItem, ok := stage.SanityPerItem(row.Times, row.Quantity)
	return rounded(perItem, ok, 3)
}

// running tells whether the counts of a row go on at now: the drop table of the stage still has the item. The matrix
// has the end of the counts that ended before it was calculated, up to a few minutes before now (see service.SEOData).
func running(row service.SEORow, now time.Time) bool {
	return !row.EndTime.Valid || row.EndTime.Int64 > now.UnixMilli()
}

// window is when the counts of a row started and ended, RFC 3339 in UTC; end is nil (null) while they go on.
func window(row service.SEORow) (start string, end *string) {
	if row.EndTime.Valid {
		e := utcTime(row.EndTime.Int64)
		end = &e
	}
	return utcTime(row.StartTime), end
}

// windowText is a window (see window) in the text content: its days, in UTC.
func windowText(start string, end *string) string {
	day := func(t string) string {
		d, _, _ := strings.Cut(t, "T")
		return d
	}
	if end == nil {
		return day(start) + " – now"
	}
	return day(start) + " – " + day(*end)
}

// sanityText is a sanity cost or sanity per item in the text content, "-" if none.
func sanityText[N int64 | float64](x *N) string {
	if x == nil {
		return "-"
	}
	return number(float64(*x))
}

// The arguments that name an item or a stage.

// candidatesShown is the most items or stages the error of an argument that names several lists
const candidatesShown = 5

// joinNotes joins the notes that are not "" into one.
func joinNotes(notes ...string) string {
	return strings.Join(slices.DeleteFunc(notes, func(n string) bool { return n == "" }), " ")
}

// candidates lists the first of the n items or stages an argument names, described.
func candidates(described []string, n int) string {
	s := strings.Join(described, "; ")
	if n > len(described) {
		s += "; and " + strconv.Itoa(n-len(described)) + " more"
	}
	return s
}

// findItem returns the item that id names: the item of that ID, or else the one item it is the name, alias or reading
// of (an exact match of the search, the only one of its tier; among the items of the types not listed if no listed item
// matches), as models pass the names they are given. The error lists the items it names otherwise, for the model to ask
// the user or pick one.
func (t *tools) findItem(c *service.SEOCatalog, id, lang string) (*service.SEOItem, error) {
	if item := c.Items[id]; item != nil {
		return item, nil
	}
	ix := t.searchIndex(c)
	var hits []itemHit
	if q, err := newQuery(id); err == nil {
		if hits = ix.searchItems(q, lang); len(hits) == 0 {
			// the items of the types not listed: a recruitment tag, a workshop material (see searchIndex.unlisted)
			hits = ix.searchUnlisted(q, lang)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("unknown item_id %q: no item has this ID, name or alias. Find the item with search_items", id)
	}
	if hits[0].how == matchExact && tied(hits) == 1 {
		return hits[0].item, nil
	}
	shown := make([]string, 0, candidatesShown)
	for _, h := range hits[:min(len(hits), candidatesShown)] {
		if service.IsListedItemType(h.item.Type) {
			shown = append(shown, h.item.ArkID+" ("+h.item.DisplayName(lang)+")")
		} else {
			shown = append(shown, h.item.ArkID+" ("+h.item.DisplayName(lang)+", "+h.item.Type+")")
		}
	}
	if hits[0].how == matchExact {
		n := tied(hits)
		return nil, fmt.Errorf("item_id %q is the name or alias of %d items: %s. Ask the user which one is meant, or pick "+
			"by context, and pass its item ID", id, n, candidates(shown[:min(n, len(shown))], n))
	}
	return nil, fmt.Errorf("unknown item_id %q: no item has this ID, name or alias, but %d items match it in part: %s. "+
		"Pass the item ID of the one meant, or find it with search_items", id, len(hits), candidates(shown, len(hits)))
}

// findStage returns the stage that id names on server: the stage of that ID, or else the one stage of that code on
// server, or anywhere if none is on server. Codes are not unique (see searchStages): the standard and tough versions
// of a stage share them, as do an event, its rerun and the stages it left open; the error lists those, with their
// zones, for the model to ask the user or pick one.
func (t *tools) findStage(c *service.SEOCatalog, id, server, lang string, now time.Time) (*service.SEOStage, error) {
	if stage := c.Stages[id]; stage != nil {
		return stage, nil
	}
	var hits []stageHit
	if q, err := newQuery(id); err == nil {
		hits = t.searchIndex(c).searchStages(q, server, now)
	}
	if len(hits) > 0 && hits[0].key.by == byID {
		// the ID in another case
		return hits[0].stage, nil
	}
	var exact, onServer []stageHit
	for _, h := range hits {
		if h.key.by == byCode && h.how == matchExact {
			exact = append(exact, h)
			if h.stage.Existence.In(server) {
				onServer = append(onServer, h)
			}
		}
	}
	switch {
	case len(onServer) == 1:
		return onServer[0].stage, nil
	case len(onServer) == 0 && len(exact) == 1:
		return exact[0].stage, nil
	}

	describe := func(hits []stageHit) string {
		shown := make([]string, 0, candidatesShown)
		for _, h := range hits[:min(len(hits), candidatesShown)] {
			s := h.stage.ArkID + " (" + h.stage.DisplayName(lang)
			if h.open {
				s += ", open now"
			}
			shown = append(shown, s+")")
		}
		return candidates(shown, len(hits))
	}
	switch {
	case len(onServer) > 1:
		return nil, fmt.Errorf("stage_id %q is the code of %d stages on %s: %s. Ask the user which one is meant, or pick by "+
			"context, and pass its stage ID", id, len(onServer), server, describe(onServer))
	case len(exact) > 1:
		return nil, fmt.Errorf("stage_id %q is the code of %d stages, none of them on %s: %s. Ask the user which one is "+
			"meant, or pick by context, and pass its stage ID", id, len(exact), server, describe(exact))
	case len(hits) > 0:
		return nil, fmt.Errorf("unknown stage_id %q: no stage has this ID or code, but %d stages match it in part (by code "+
			"or zone): %s. Pass the stage ID of the one meant, or find it with search_stages", id, len(hits), describe(hits))
	}
	return nil, fmt.Errorf("unknown stage_id %q: no stage has this ID or code. Find the stage with search_stages", id)
}
