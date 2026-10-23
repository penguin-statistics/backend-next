package mcp

import (
	"context"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchStagesIn struct {
	Query  string `json:"query" jsonschema:"Code or ID of the stage, or the name or ID of its zone, in any language: 1-7, S2-12, main_01-07, 第十章 or Under Tides"`
	Server string `json:"server,omitempty"`
	Lang   string `json:"lang,omitempty"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Most stages to return, 1 to 25. Default 10."`
}

type stageSearch struct {
	Query     string       `json:"query"`
	Server    string       `json:"server" jsonschema:"Game server of openNow"`
	Results   []stageMatch `json:"results" jsonschema:"The stages found, best match first"`
	Ambiguous bool         `json:"ambiguous" jsonschema:"Whether several stages share the code (or ID) that matched best: the standard and tough versions of a stage, or an event, its rerun and the stage it left open; ask which one is meant, or pick by context, rather than take the first. False when the query matched a zone: its stages are listed."`
	Truncated bool         `json:"truncated" jsonschema:"Whether more stages match than the limit"`
	Source    source       `json:"source"`
}

type stageMatch struct {
	stageRef
	Difficulty string `json:"difficulty" jsonschema:"normal, or tough: the tough version of a stage of the main story, with the same code"`
	Variant    string `json:"variant" jsonschema:"original, rerun (the stage of the rerun of an event) or permanent (the stage an event left open for good)"`
	// one of byID, byCode and byZone
	MatchedBy   string `json:"matchedBy" jsonschema:"What matched the query: id (of the stage), code (in any language) or zone (its name in any language, or its ID)"`
	MatchType   string `json:"matchType" jsonschema:"How matchedText matched the query: exact, prefix or substring"`
	MatchedText string `json:"matchedText"`
}

func searchStagesTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "search_stages",
		Title: "Search stages",
		Description: "Finds stages by code (1-7, S2-12, YW-4; full-width characters work too), by stage ID " +
			"(main_01-07), or by the name or ID of their zone (第十章, Under Tides), and returns their stage IDs (for " +
			"get_stage_drops; to find the stages that drop an item, use get_item_drop_rates) with their zone, sanity " +
			"cost and whether they are open on the server now. Codes are not " +
			"unique: the standard and tough versions of a stage of the main story share them, as do an event, its rerun " +
			"and the stages it left open for good. Best matches first (the code before the zone, exact before prefix " +
			"before substring), then the stages open now, standard before tough, original before permanent before rerun. " +
			"ambiguous is true when several stages share the best-matching code (not for the stages of a zone found by " +
			"its name): ask the user which one is meant or decide by context.",
		InputSchema: inputSchema[searchStagesIn](map[string]property{
			"query": lengthProperty(1, queryMaxLength),
			"server": func(p *jsonschema.Schema) {
				rankingServerProperty(p)
				p.Description += " The stages open there now rank first."
			},
			"lang":  langProperty,
			"limit": intProperty(searchLimit, 1, searchMaxLimit),
		}),
	}
}

func (t *tools) searchStages(_ context.Context, _ *sdkmcp.CallToolRequest, in searchStagesIn) (*sdkmcp.CallToolResult, stageSearch, error) {
	c, err := t.catalog()
	if err != nil {
		return nil, stageSearch{}, err
	}
	q, err := newQuery(in.Query)
	if err != nil {
		return nil, stageSearch{}, err
	}
	server, lang := serverOf(in.Server), langOf(in.Lang)
	now := t.now()
	hits := t.searchIndex(c).searchStages(q, server, now)
	shown := hits[:min(len(hits), limitOf(in.Limit, searchLimit, searchMaxLimit))]

	src := t.source("")
	out := stageSearch{
		Query:   in.Query,
		Server:  server,
		Results: make([]stageMatch, 0, len(shown)),
		// the stages of a zone found by its name are a list, not a choice
		Ambiguous: tied(hits) > 1 && hits[0].key.by != byZone,
		Truncated: len(shown) < len(hits),
		Source:    src,
	}
	rows := make([][]string, 0, len(shown))
	rotation := false
	for _, h := range shown {
		stage := h.stage
		m := stageMatch{
			stageRef:    t.stageRef(stage, server, lang, t.data.Matrix(server), now),
			Difficulty:  "normal",
			Variant:     variantOf(stage),
			MatchedBy:   h.key.by,
			MatchType:   h.how,
			MatchedText: h.key.text,
		}
		if isTough(stage) {
			m.Difficulty = "tough"
		}
		out.Results = append(out.Results, m)

		kind := m.ZoneType
		if m.Difficulty != "normal" {
			kind += ", " + m.Difficulty
		}
		if m.Variant != variantOriginal {
			kind += ", " + m.Variant
		}
		ap, open := "", yesNo(m.OpenNow)
		if m.APCost != nil {
			ap = strconv.FormatInt(*m.APCost, 10)
		}
		if m.OpenNow && rotates(stage) {
			open += "*"
			rotation = true
		}
		rows = append(rows, []string{m.StageID, m.Code, m.ZoneName, kind, ap, open, h.describe()})
	}

	lines := []string{searchSummary("stage", in.Query, hits, len(shown), out.Ambiguous,
		"Try a stage code such as 1-7, a stage ID such as main_01-07, or the name of the zone in another language.",
		"ask which one is meant, or pick by context (the stage open now, the standard version, the original event).")}
	if len(rows) > 0 {
		lines = append(lines, "", table([]string{"stageId", "code", "zone", "kind", "sanity", "open on " + server + " now", "matched by"}, rows))
	}
	if rotation {
		lines = append(lines, "", "* "+rotationCaveat)
	}
	lines = append(lines, "", src.line())
	return textResult(lines...), out, nil
}
