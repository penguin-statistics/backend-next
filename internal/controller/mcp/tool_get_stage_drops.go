package mcp

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

// maxStageRows is the most items of a stage to return, of a stage of many more than any has: the recruitment has the
// most, 29 tags
const maxStageRows = 100

type getStageDropsIn struct {
	StageID string `json:"stage_id" jsonschema:"Stage ID, from search_stages: main_01-07. A code works too if only one stage on the server has it: 1-7"`
	Server  string `json:"server,omitempty"`
	Lang    string `json:"lang,omitempty"`
}

type stageDrops struct {
	Stage       stageRef    `json:"stage"`
	Server      string      `json:"server"`
	DataAsOf    string      `json:"dataAsOf,omitempty" jsonschema:"When the drop data was calculated, RFC 3339 in UTC"`
	Rows        []itemRate  `json:"rows" jsonschema:"The items the stage drops, highest rate first; then those of windows that ended (end not null), which it no longer drops, such as the bonus drops of past events"`
	Truncated   bool        `json:"truncated" jsonschema:"Whether items were left out, past 100"`
	Note        string      `json:"note,omitempty"`
	Definitions definitions `json:"definitions"`
	Source      source      `json:"source"`
}

// itemRate is a row of a stage: an item it drops.
type itemRate struct {
	ItemID        string   `json:"itemId"`
	ItemName      string   `json:"itemName" jsonschema:"Name in the language asked for"`
	Times         int      `json:"times"`
	Quantity      int      `json:"quantity"`
	DropRate      float64  `json:"dropRate"`
	SanityPerItem *float64 `json:"sanityPerItem"`
	Start         string   `json:"start" jsonschema:"Start of the window, RFC 3339 in UTC"`
	End           *string  `json:"end" jsonschema:"End of the window, RFC 3339 in UTC; null while it goes on"`
}

func getStageDropsTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "get_stage_drops",
		Title: "Stage drops",
		Description: "Returns what a stage drops on a game server: each item with runs (times), quantity, drop rate " +
			"(items per run) and sanity per item, highest rate first, then the items it no longer drops; with the " +
			"sanity cost of the stage and whether it is open now. Takes the stage ID from search_stages (codes are " +
			"shared by several stages); a code works too if only one stage on the server has it.",
		InputSchema: inputSchema[getStageDropsIn](map[string]property{
			"stage_id": lengthProperty(1, queryMaxLength),
			"server":   serverProperty,
			"lang":     langProperty,
		}),
	}
}

func (t *tools) getStageDrops(_ context.Context, _ *sdkmcp.CallToolRequest, in getStageDropsIn) (*sdkmcp.CallToolResult, stageDrops, error) {
	c, err := t.catalog()
	if err != nil {
		return nil, stageDrops{}, err
	}
	server, lang := serverOf(in.Server), langOf(in.Lang)
	now := t.now()
	stage, err := t.findStage(c, in.StageID, server, lang, now)
	if err != nil {
		return nil, stageDrops{}, err
	}
	m, err := t.matrix(server)
	if err != nil {
		return nil, stageDrops{}, err
	}

	// by rate (see service.SEOMatrix), the windows that ended last: the bonus drops of the events of the past would
	// rank among the drops of a stage of the main story otherwise
	all := slices.Clone(m.ByStage[stage.ArkID])
	slices.SortStableFunc(all, func(a, b service.SEORow) int {
		return compareBool(!running(a, now), !running(b, now))
	})
	rows := make([]itemRate, 0, min(len(all), maxStageRows))
	ended, total := 0, 0
	for _, row := range all {
		if !counted(row) {
			continue
		}
		total++
		if len(rows) == maxStageRows {
			continue
		}
		if !running(row, now) {
			ended++
		}
		rows = append(rows, itemRateOf(c.Items[row.ItemID], stage, row, lang))
	}

	ref, name := t.stageRef(stage, server, lang, m, now), stage.DisplayName(lang)
	pageURL := ""
	if ref.PageURL != nil {
		pageURL = *ref.PageURL
	}
	out := stageDrops{
		Stage:       ref,
		Server:      server,
		DataAsOf:    dataAsOf(m),
		Rows:        rows,
		Truncated:   len(rows) < total,
		Note:        stageNote(stage, ref, name, server, len(rows), ended),
		Definitions: dropDefinitions,
		Source:      t.source(pageURL),
	}
	return textResult(stageDropsLines(out, name)...), out, nil
}

// itemRateOf is the row of the item (nil if the catalog does not have it) of the stage, in lang.
func itemRateOf(item *service.SEOItem, stage *service.SEOStage, row service.SEORow, lang string) itemRate {
	start, end := window(row)
	r := itemRate{
		ItemID:        row.ItemID,
		ItemName:      row.ItemID,
		Times:         row.Times,
		Quantity:      row.Quantity,
		DropRate:      dropRate(row),
		SanityPerItem: sanityPerItem(stage, row),
		Start:         start,
		End:           end,
	}
	if item != nil {
		r.ItemName = item.DisplayName(lang)
	}
	return r
}

// stageNote tells how to read the n rows of the stage named name, of which ended have windows that ended, or why there
// are none; "" if there is nothing to tell.
func stageNote(stage *service.SEOStage, ref stageRef, name, server string, n, ended int) string {
	var notes []string
	switch {
	case stage.IsGacha:
		notes = append(notes, "The stage is a gacha: times count the items drawn, so a drop rate is per draw, and it has no sanity cost.")
	case service.IsRecruitStage(stage):
		notes = append(notes, "This is the recruitment: times count recruitments and the items are the tags offered, so a drop rate is per recruitment; it has no sanity cost.")
	}
	switch {
	case n == 0:
		s := fmt.Sprintf("No drop of %s is recorded on %s.", name, server)
		if !stage.Existence.In(server) {
			s += fmt.Sprintf(" The stage does not exist on %s", server)
			if len(ref.ExistsOn) > 0 {
				s += fmt.Sprintf(": ask for it on %s", strings.Join(ref.ExistsOn, ", "))
			}
			s += "."
		}
		notes = append(notes, s)
	case !ref.OpenNow:
		notes = append(notes, fmt.Sprintf("The stage is not open on %s now: these are the drops of when it was.", server))
	case ended == 1:
		notes = append(notes, "The last item is no longer dropped: its window ended (end is not null), as the drop table "+
			"of the stage changed or the event of a bonus drop ended.")
	case ended > 1:
		notes = append(notes, fmt.Sprintf("The last %d items are no longer dropped: their windows ended (end is not null), "+
			"as the drop table of the stage changed or the events of bonus drops ended.", ended))
	}
	if ref.OpenNow && rotates(stage) {
		notes = append(notes, rotationCaveat)
	}
	return strings.Join(notes, " ")
}

// stageDropsLines are the text content of the drops of a stage named name.
func stageDropsLines(out stageDrops, name string) []string {
	s := out.Stage
	head := "Drops of " + name + " (" + s.StageID + ") on " + out.Server
	if out.DataAsOf != "" {
		head += ", data as of " + out.DataAsOf
	}
	head += "."
	if s.APCost != nil {
		head += fmt.Sprintf(" Sanity %d per run.", *s.APCost)
	}
	if s.OpenNow {
		head += " Open now."
	} else {
		head += " Not open now."
	}
	if len(out.Rows) > 0 {
		head += " " + count(len(out.Rows), "item") + ", highest drop rate first."
	}
	lines := []string{head}

	if len(out.Rows) > 0 {
		rows := make([][]string, 0, len(out.Rows))
		for _, r := range out.Rows {
			rows = append(rows, []string{r.ItemID, r.ItemName, strconv.Itoa(r.Times), strconv.Itoa(r.Quantity), number(r.DropRate),
				sanityText(r.SanityPerItem), windowText(r.Start, r.End)})
		}
		lines = append(lines, "", table([]string{"itemId", "item", "times", "quantity", "drop rate", "sanity per item", "window (UTC)"}, rows),
			"", rateDefinitions)
	}
	if out.Note != "" {
		lines = append(lines, "", out.Note)
	}
	return append(lines, "", out.Source.line())
}
