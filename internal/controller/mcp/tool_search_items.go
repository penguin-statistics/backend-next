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

type searchItemsIn struct {
	Query string `json:"query" jsonschema:"Name, alias, pinyin or ID of the item, in any language: 固源岩, Orirock Cube, 石头, guyuanyan or 30012"`
	Lang  string `json:"lang,omitempty"`
	Limit int    `json:"limit,omitempty" jsonschema:"Most items to return, 1 to 25. Default 10."`
}

type itemSearch struct {
	Query     string      `json:"query"`
	Results   []itemMatch `json:"results" jsonschema:"The items found, best match first"`
	Ambiguous bool        `json:"ambiguous" jsonschema:"Whether several items match as well as the first (an alias of several items, such as 石头 for the four tiers of Orirock): ask which one is meant, or pick by context, rather than take the first"`
	Truncated bool        `json:"truncated" jsonschema:"Whether more items match than the limit"`
	Note      string      `json:"note,omitempty" jsonschema:"Set when no item of a listed type matched and the results are items of other types: what those are"`
	Source    source      `json:"source"`
}

type itemMatch struct {
	ItemID string    `json:"itemId"`
	Name   string    `json:"name" jsonschema:"Name in the language asked for"`
	Names  itemNames `json:"names"`
	Type   string    `json:"type" jsonschema:"MATERIAL, CARD_EXP (battle records), CHIP, FURN (furniture) or ACTIVITY_ITEM (event items); with a note, another type: ARKPLANNER (crafted in the workshop), RECRUIT_TAG (a recruitment tag), TEMP or LGG_SHD"`
	Rarity int       `json:"rarity" jsonschema:"Rarity as stored, 0 to 4: the tier of the item in the game minus 1"`
	// one of the by* constants but byCode and byZone
	MatchedBy   string   `json:"matchedBy" jsonschema:"What matched the query: id, name (in any language), alias (a nickname in zh or ja), pinyin (a reading in zh) or romaji (a reading in ja)"`
	MatchType   string   `json:"matchType" jsonschema:"How matchedText matched the query: exact, prefix or substring"`
	MatchedText string   `json:"matchedText"`
	ExistsOn    []string `json:"existsOn" jsonschema:"Game servers the item exists on"`
	PageURL     *string  `json:"pageUrl" jsonschema:"Page of the drop rates of the item on Penguin Statistics, on CN or else the first server it exists on; null if it exists on none; null for an item of another type"`
}

// itemNames are the names of an item in each language.
type itemNames struct {
	Zh string `json:"zh"`
	En string `json:"en"`
	Ja string `json:"ja"`
	Ko string `json:"ko"`
}

func searchItemsTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "search_items",
		Title: "Search items",
		Description: "Finds items (materials, battle records, chips, furniture, event items) by name in any language " +
			"(zh, en, ja, ko), by alias or nickname (土块, 狗粮), by pinyin (guyuanyan) or by item ID, and returns their " +
			"item IDs (get_item_drop_rates takes a name too; search first when unsure which item is meant) and names in " +
			"every language. Best matches first: exact before prefix " +
			"before substring, names before aliases. Several items can share an alias (石头 is each tier of Orirock): " +
			"ambiguous is then true; ask the user which one is meant or decide by context. Items of other types " +
			"(workshop-crafted materials such as D32 Steel, recruitment tags) are returned only when no listed item " +
			"matches, with a note saying what they are.",
		InputSchema: inputSchema[searchItemsIn](map[string]property{
			"query": lengthProperty(1, queryMaxLength),
			"lang":  langProperty,
			"limit": intProperty(searchLimit, 1, searchMaxLimit),
		}),
	}
}

func (t *tools) searchItems(_ context.Context, _ *sdkmcp.CallToolRequest, in searchItemsIn) (*sdkmcp.CallToolResult, itemSearch, error) {
	c, err := t.catalog()
	if err != nil {
		return nil, itemSearch{}, err
	}
	q, err := newQuery(in.Query)
	if err != nil {
		return nil, itemSearch{}, err
	}
	lang := langOf(in.Lang)
	ix := t.searchIndex(c)
	hits := ix.searchItems(q, lang)
	if len(hits) == 0 {
		hits = ix.searchUnlisted(q, lang)
	}
	shown := hits[:min(len(hits), limitOf(in.Limit, searchLimit, searchMaxLimit))]

	src := t.source("")
	out := itemSearch{
		Query:     in.Query,
		Results:   make([]itemMatch, 0, len(shown)),
		Ambiguous: tied(hits) > 1,
		Truncated: len(shown) < len(hits),
		Source:    src,
	}
	rows := make([][]string, 0, len(shown))
	for _, h := range shown {
		item := h.item
		m := itemMatch{
			ItemID:      item.ArkID,
			Name:        item.DisplayName(lang),
			Names:       itemNames{Zh: item.DisplayName("zh"), En: item.DisplayName("en"), Ja: item.DisplayName("ja"), Ko: item.DisplayName("ko")},
			Type:        item.Type,
			Rarity:      item.Rarity,
			MatchedBy:   h.key.by,
			MatchType:   h.how,
			MatchedText: h.key.text,
			ExistsOn:    existsOn(item.Existence),
		}
		if len(m.ExistsOn) > 0 && service.IsListedItemType(item.Type) {
			m.PageURL = link(service.ItemPageURL(t.site, lang, m.ExistsOn[0], item.ArkID))
		}
		out.Results = append(out.Results, m)
		rows = append(rows, []string{m.ItemID, m.Name, m.Type, strconv.Itoa(m.Rarity), h.describe()})
	}
	if len(shown) > 0 && !service.IsListedItemType(shown[0].item.Type) {
		// no listed item matched: say what these are
		var notes []string
		for _, h := range shown {
			if n := typeNote(h.item.Type); n != "" && !slices.Contains(notes, n) {
				notes = append(notes, n)
			}
		}
		out.Note = fmt.Sprintf("No item of the types Penguin Statistics lists drop rates for (materials, battle records, "+
			"chips, furniture, event items) matches %q: these items of other types do. ", in.Query) + strings.Join(notes, " ")
	}

	lines := []string{searchSummary("item", in.Query, hits, len(shown), out.Ambiguous,
		"Try its name in another language (zh, en, ja or ko), an alias, or fewer characters.",
		"ask which one is meant, or pick by context.")}
	if len(rows) > 0 {
		lines = append(lines, "", table([]string{"itemId", "name", "type", "rarity (0-4)", "matched by"}, rows))
	}
	if out.Note != "" {
		lines = append(lines, "", out.Note)
	}
	lines = append(lines, "", src.line())
	return textResult(lines...), out, nil
}
