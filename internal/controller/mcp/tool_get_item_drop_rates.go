package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

// The orders of the stages of an item, the values of sort_by
const (
	sortBySanity = "sanity_per_item"
	sortByRate   = "drop_rate"
)

// The number of stages of an item to return: furniture, dropped by the most, by 462 open on CN in October 2026, about
// 20k tokens in all at about 50 a row
const (
	topN    = 10
	maxTopN = 100
)

type getItemDropRatesIn struct {
	ItemID        string `json:"item_id" jsonschema:"Item ID, from search_items: 30012. A name or alias that only one item has works too: 固源岩, Orirock Cube"`
	Server        string `json:"server,omitempty"`
	TopN          int    `json:"top_n,omitempty" jsonschema:"Most stages to return, 1 to 100. Default 10."`
	IncludeClosed bool   `json:"include_closed,omitempty" jsonschema:"false (default): only the stages open on the server now that still drop the item. true: also the stages of past events and the drops that ended, for history."`
	SortBy        string `json:"sort_by,omitempty"`
	Lang          string `json:"lang,omitempty"`
}

type itemDropRates struct {
	Item          itemRef     `json:"item"`
	Server        string      `json:"server"`
	DataAsOf      string      `json:"dataAsOf,omitempty" jsonschema:"When the drop data was calculated, RFC 3339 in UTC"`
	IncludeClosed bool        `json:"includeClosed"`
	SortBy        string      `json:"sortBy"`
	TotalStages   int         `json:"totalStages" jsonschema:"Stages that drop the item now (dropsNow), or all with includeClosed"`
	Returned      int         `json:"returned"`
	Truncated     bool        `json:"truncated" jsonschema:"Whether top_n left out stages"`
	Rows          []stageRate `json:"rows" jsonschema:"The stages, in the order of sortBy"`
	Note          string      `json:"note,omitempty"`
	Definitions   definitions `json:"definitions"`
	Source        source      `json:"source"`
}

type itemRef struct {
	ItemID  string  `json:"itemId"`
	Name    string  `json:"name" jsonschema:"Name in the language asked for"`
	Type    string  `json:"type"`
	PageURL *string `json:"pageUrl" jsonschema:"Page of the drop rates of the item on the server on Penguin Statistics; null if there is none"`
}

// stageRate is a row of an item: a stage that drops it.
type stageRate struct {
	StageID       string   `json:"stageId"`
	Code          string   `json:"code" jsonschema:"Code in the language asked for"`
	ZoneName      string   `json:"zoneName" jsonschema:"Name of the zone in the language asked for"`
	APCost        *int64   `json:"apCost" jsonschema:"Sanity cost of a run; null without one (gachas, the recruitment)"`
	Times         int      `json:"times"`
	Quantity      int      `json:"quantity"`
	DropRate      float64  `json:"dropRate"`
	SanityPerItem *float64 `json:"sanityPerItem"`
	Start         string   `json:"start" jsonschema:"Start of the window, RFC 3339 in UTC"`
	End           *string  `json:"end" jsonschema:"End of the window, RFC 3339 in UTC; null while it goes on"`
	OpenNow       bool     `json:"openNow" jsonschema:"Whether the stage is open on the server now, by the open and close times of the stage and its zone; supply and chip stages (zoneType WEEKLY) count as open every day, as their weekday rotation is not tracked"`
	DropsNow      bool     `json:"dropsNow" jsonschema:"Whether the stage is open now and still drops the item (end is null or later than now): the rows of the default result"`
	LowSample     bool     `json:"lowSample" jsonschema:"Whether the rate is of fewer than 100 runs, and so noisy"`
	PageURL       *string  `json:"pageUrl" jsonschema:"Page of the drops of the stage on the server on Penguin Statistics; null if there is none"`
}

func getItemDropRatesTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "get_item_drop_rates",
		Title: "Item drop rates",
		Description: "Returns where to farm an item on a game server: the stages that drop it, with runs (times), " +
			"quantity, drop rate (items per run) and sanity per item. By default the stages open now that still drop " +
			"it, lowest sanity per item first; stages of fewer than 100 runs (lowSample) rank after the others, " +
			"gachas and the recruitment (no sanity cost) last. sort_by=drop_rate ranks by rate instead; " +
			"include_closed=true adds past events and ended drops. Takes the item ID from search_items.",
		InputSchema: inputSchema[getItemDropRatesIn](map[string]property{
			"item_id": lengthProperty(1, queryMaxLength),
			"server":  serverProperty,
			"top_n":   intProperty(topN, 1, maxTopN),
			"include_closed": func(p *jsonschema.Schema) {
				p.Default = json.RawMessage("false")
			},
			"sort_by": func(p *jsonschema.Schema) {
				p.Description = "sanity_per_item (default): the least sanity per item first. drop_rate: the most items " +
					"per run first."
				p.Enum = []any{sortBySanity, sortByRate}
				p.Default = json.RawMessage(strconv.Quote(sortBySanity))
			},
			"lang": langProperty,
		}),
	}
}

func (t *tools) getItemDropRates(_ context.Context, _ *sdkmcp.CallToolRequest, in getItemDropRatesIn) (*sdkmcp.CallToolResult, itemDropRates, error) {
	c, err := t.catalog()
	if err != nil {
		return nil, itemDropRates{}, err
	}
	server, lang := serverOf(in.Server), langOf(in.Lang)
	item, err := t.findItem(c, in.ItemID, lang)
	if err != nil {
		return nil, itemDropRates{}, err
	}
	m, err := t.matrix(server)
	if err != nil {
		return nil, itemDropRates{}, err
	}
	sortBy := cmp.Or(in.SortBy, sortBySanity)
	now := t.now()

	// the rows of the stages open now that still drop the item, the frontend's matrix without the closed zones and the
	// drops that ended, or all of them: the matrix has those of the closed zones too (see service.SEOData.Matrix). By
	// rate, the order of drop_rate.
	all := m.ByItem[item.ArkID]
	rows := make([]stageRate, 0, len(all))
	for _, row := range all {
		if !counted(row) {
			continue
		}
		r := t.stageRateOf(c.Stages[row.StageID], row, server, lang, now)
		if r.DropsNow || in.IncludeClosed {
			rows = append(rows, r)
		}
	}
	if sortBy == sortBySanity {
		slices.SortStableFunc(rows, func(a, b stageRate) int {
			if c := cmp.Compare(sanityRank(a), sanityRank(b)); c != 0 || a.SanityPerItem == nil {
				return c
			}
			return cmp.Compare(*a.SanityPerItem, *b.SanityPerItem)
		})
	}
	shown := rows[:min(len(rows), limitOf(in.TopN, topN, maxTopN))]

	pageURL, hasPage := "", false
	if service.ItemOnServer(item, server, m) {
		pageURL, hasPage = service.ItemPageURL(t.site, lang, server, item.ArkID)
	}
	out := itemDropRates{
		Item:          itemRef{ItemID: item.ArkID, Name: item.DisplayName(lang), Type: item.Type, PageURL: link(pageURL, hasPage)},
		Server:        server,
		DataAsOf:      dataAsOf(m),
		IncludeClosed: in.IncludeClosed,
		SortBy:        sortBy,
		TotalStages:   len(rows),
		Returned:      len(shown),
		Truncated:     len(shown) < len(rows),
		Rows:          shown,
		Definitions:   dropDefinitions,
		Source:        t.source(pageURL),
	}
	rotation := ""
	if slices.ContainsFunc(shown, func(r stageRate) bool { return r.DropsNow && rotates(c.Stages[r.StageID]) }) {
		rotation = rotationCaveat
	}
	out.Note = joinNotes(typeNote(item.Type), itemNote(item, out, len(all)), rotation)
	return textResult(itemDropRatesLines(out)...), out, nil
}

// stageRateOf is the row of the stage (nil if the catalog does not have it) for the item, on server at now, in lang.
func (t *tools) stageRateOf(stage *service.SEOStage, row service.SEORow, server, lang string, now time.Time) stageRate {
	start, end := window(row)
	r := stageRate{
		StageID:       row.StageID,
		Code:          row.StageID,
		Times:         row.Times,
		Quantity:      row.Quantity,
		DropRate:      dropRate(row),
		SanityPerItem: sanityPerItem(stage, row),
		Start:         start,
		End:           end,
		LowSample:     row.Times < minSamples,
	}
	if stage == nil {
		return r
	}
	r.Code, r.ZoneName = stage.DisplayCode(lang), stage.Zone.DisplayName(lang)
	if sanity, ok := stage.SanityCost(); ok {
		r.APCost = &sanity
	}
	r.OpenNow = stageOpen(stage, server, now)
	r.DropsNow = r.OpenNow && running(row, now)
	r.PageURL = link(service.StagePageURL(t.site, lang, server, stage))
	return r
}

// sanityRank ranks the rows by sanity per item: those of enough runs first, then those of few runs, whose sanity per
// item is noisy (the best of 30061 on CN was of 4 runs), then those without a sanity per item, by rate.
func sanityRank(r stageRate) int {
	switch {
	case r.SanityPerItem == nil:
		return 2
	case r.LowSample:
		return 1
	}
	return 0
}

// itemNote tells why the drop rates of the item have no rows, or how to read those of few runs; "" if there is nothing
// to tell. dropped is the number of rows of the item in the matrix, open or not.
func itemNote(item *service.SEOItem, out itemDropRates, dropped int) string {
	switch {
	case dropped == 0:
		s := fmt.Sprintf("No drop of %s is recorded on %s: no stage drops it there, or Penguin Statistics does not record it.",
			out.Item.Name, out.Server)
		if !item.Existence.In(out.Server) {
			s += fmt.Sprintf(" The item does not exist on %s.", out.Server)
		}
		return s
	case len(out.Rows) == 0 && !out.IncludeClosed:
		return fmt.Sprintf("No stage open on %s now drops %s, but %d did: set include_closed to true to see them.",
			out.Server, out.Item.Name, dropped)
	case slices.ContainsFunc(out.Rows, func(r stageRate) bool { return r.LowSample }):
		s := fmt.Sprintf("Rates of fewer than %d runs (lowSample) are noisy", minSamples)
		if out.SortBy == sortBySanity {
			return s + ": they rank after the others by sanity per item."
		}
		return s + "."
	}
	return ""
}

// itemDropRatesLines are the text content of the drop rates of an item.
func itemDropRatesLines(out itemDropRates) []string {
	head := "Drop rates of " + out.Item.Name + " (" + out.Item.ItemID + ") on " + out.Server
	if out.DataAsOf != "" {
		head += ", data as of " + out.DataAsOf
	}
	head += ". "
	switch {
	case out.IncludeClosed:
		head += count(out.TotalStages, "stage") + " dropped it, open now or not"
	case out.TotalStages == 1:
		head += "1 stage open now drops it"
	default:
		head += count(out.TotalStages, "stage") + " open now drop it"
	}
	if out.Returned > 0 {
		if out.Truncated {
			head += fmt.Sprintf("; the first %d", out.Returned)
		}
		switch {
		case out.Returned == 1:
			// one row: no order to tell
		case out.SortBy == sortByRate:
			head += ", highest drop rate first"
		default:
			head += ", lowest sanity per item first (few runs, then no sanity cost last)"
		}
	}
	head += "."
	if out.Truncated {
		head += fmt.Sprintf(" Raise top_n (at most %d) for more.", maxTopN)
	}
	lines := []string{head}

	if len(out.Rows) > 0 {
		header := []string{"stageId", "code", "zone", "sanity", "times", "quantity", "drop rate", "sanity per item", "window (UTC)"}
		if out.IncludeClosed {
			header = append(header, "drops now")
		}
		rows := make([][]string, 0, len(out.Rows))
		for _, r := range out.Rows {
			times := strconv.Itoa(r.Times)
			if r.LowSample {
				times += " (few runs)"
			}
			row := []string{r.StageID, r.Code, r.ZoneName, sanityText(r.APCost), times, strconv.Itoa(r.Quantity),
				number(r.DropRate), sanityText(r.SanityPerItem), windowText(r.Start, r.End)}
			if out.IncludeClosed {
				row = append(row, yesNo(r.DropsNow))
			}
			rows = append(rows, row)
		}
		lines = append(lines, "", table(header, rows), "", rateDefinitions)
	}
	if out.Note != "" {
		lines = append(lines, "", out.Note)
	}
	return append(lines, "", out.Source.line())
}

// rateDefinitions define the numbers of the tables of the text content, for the clients that read it only
var rateDefinitions = strings.Join([]string{
	"drop rate = quantity ÷ times: items per run (can exceed 1).",
	"sanity per item = sanity ÷ drop rate.",
	"window: since the drop table of the stage last changed for the item.",
}, " ")
