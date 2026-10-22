package mcp

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"exusiai.dev/backend-next/internal/service"
)

// The search of the items and stages by what users and models call them: the names of the items in every language,
// their aliases and readings, the codes of the stages and the names of their zones. The keys of a catalog are made
// once (see tools.searchIndex); a search compares the query with each of them, a few thousand short strings.

// How a key matches a query, from the best match to the worst
const (
	matchExact     = "exact"
	matchPrefix    = "prefix"
	matchSubstring = "substring"
)

// What a key of a result is, its matchedBy
const (
	byID     = "id"
	byName   = "name"
	byAlias  = "alias"
	byPinyin = "pinyin"
	byRomaji = "romaji"
	byCode   = "code"
	byZone   = "zone"
)

// fold is s as the search compares IDs: in NFKC, which turns the full-width letters, digits and punctuation of CJK
// input methods into ASCII ("１－７" is "1-7"), case-folded, its dashes "-", trimmed.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Pd, r) || r == '−' {
			return '-'
		}
		return r
	}, strings.TrimSpace(cases.Fold().String(norm.NFKC.String(s))))
}

// nameKey is s as the search compares names: fold(s) without the separators users write or leave out at will (spaces,
// "-", "_", "·" and "・"), so that "Orirock Cube", "orirockcube" and "ORIROCK-CUBE" are one name.
func nameKey(s string) string {
	return strip(fold(s), true)
}

// codeKey is s as the search compares stage codes: as nameKey, but with the dashes kept, as 1-12 and 11-2 are two
// stages.
func codeKey(s string) string {
	return strip(fold(s), false)
}

func strip(s string, dashes bool) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' || r == '·' || r == '・' || (dashes && r == '-') {
			return -1
		}
		return r
	}, s)
}

// query is the query of a search as compared with IDs, names and codes.
type query struct {
	id, name, code string
}

// newQuery returns the query of text, or an error if it has nothing to compare: only spaces and separators.
func newQuery(text string) (query, error) {
	q := query{id: fold(text), name: nameKey(text), code: codeKey(text)}
	if q.name == "" {
		return q, fmt.Errorf("nothing to search for in the query %q: give a name, an alias, a code or an ID", text)
	}
	return q, nil
}

// searchKey is a text an item or stage is found by.
type searchKey struct {
	// key is the text as compared (see nameKey and codeKey)
	key string
	// text is the text as given in the catalog, the matchedText of a result
	text string
	// by is what the text is, the matchedBy of a result
	by string
	// prefixOnly keys do not match by substring: a few letters of a reading are in many
	prefixOnly bool
}

// appendKey appends the key of text by keyOf to keys, unless it is empty or keys has it already.
func appendKey(keys []searchKey, keyOf func(string) string, text, by string) []searchKey {
	k := keyOf(text)
	if k == "" || slices.ContainsFunc(keys, func(o searchKey) bool { return o.key == k }) {
		return keys
	}
	return append(keys, searchKey{key: k, text: text, by: by, prefixOnly: by == byPinyin || by == byRomaji})
}

// hit is how a query matched an item or stage: by its key that matched best.
type hit struct {
	// tier ranks the hits, from 0 (an exact ID): see itemKeys.match and stageKeys.match
	tier int
	key  searchKey
	// how is matchExact, matchPrefix or matchSubstring
	how string
}

// describe is the hit in the text content of a result, e.g. "alias, exact: 石头".
func (h hit) describe() string {
	return h.key.by + ", " + h.how + ": " + h.key.text
}

func (h hit) tierOf() int {
	return h.tier
}

// rank is the rank of how among the matches: 0 for an exact one.
func rank(how string) int {
	return slices.Index([]string{matchExact, matchPrefix, matchSubstring}, how)
}

// bestMatch returns the best match of q among keys, the first key of the best kind of match; how is "" if none
// matches.
func bestMatch(keys []searchKey, q string) (k searchKey, how string) {
	for _, key := range keys {
		var m string
		switch {
		case key.key == q:
			return key, matchExact
		case strings.HasPrefix(key.key, q):
			m = matchPrefix
		case !key.prefixOnly && strings.Contains(key.key, q):
			m = matchSubstring
		default:
			continue
		}
		if how == "" || rank(m) < rank(how) {
			k, how = key, m
		}
	}
	return k, how
}

// ranked is an item or stage hit.
type ranked interface {
	tierOf() int
	describe() string
}

// tied is how many of hits, ordered best first, match as well as the first.
func tied[H ranked](hits []H) int {
	n := 0
	for n < len(hits) && hits[n].tierOf() == hits[0].tierOf() {
		n++
	}
	return n
}

// The limits of the results of a search: the largest group of items that share an alias has 7, the largest of
// stages that share a code 4
const (
	searchLimit    = 10
	searchMaxLimit = 25
	// queryMaxLength is the most characters of a query, more than any name has
	queryMaxLength = 64
)

// searchSummary is the first line of the text content of a search for nouns (item or stage) matching text, of
// which the first shown are the results: how many match, what to do if the result is ambiguous, several matching as
// well as the first (tiedHint), or none matches (noneHint), and whether there are more.
func searchSummary[H ranked](noun, text string, hits []H, shown int, ambiguous bool, noneHint, tiedHint string) string {
	switch len(hits) {
	case 0:
		return fmt.Sprintf("No %s matches %q. %s", noun, text, noneHint)
	case 1:
		return fmt.Sprintf("1 %s matches %q.", noun, text)
	}
	s := fmt.Sprintf("%d %ss match %q.", len(hits), noun, text)
	if n := tied(hits); ambiguous && n > 1 {
		s += fmt.Sprintf(" The first %d match equally well (%s): %s", n, hits[0].describe(), tiedHint)
	}
	if shown < len(hits) {
		s += fmt.Sprintf(" Showing the first %d: narrow the query, or raise limit (at most %d).", shown, searchMaxLimit)
	}
	return s
}

// searchIndex holds the keys of the items and stages of a catalog.
type searchIndex struct {
	catalog *service.SEOCatalog
	// items are the items the frontend lists, by sort ID
	items []itemKeys
	// unlisted are the items of the types the frontend does not list (workshop materials, recruitment tags...), by
	// sort ID: searched only when no listed item matches
	unlisted []itemKeys
	// stages are by stage ID
	stages []stageKeys
}

type itemKeys struct {
	item *service.SEOItem
	id   string
	// names are the names in each language
	names []searchKey
	// aliases are the aliases of the item (zh and ja only, October 2026) and their readings
	aliases []searchKey
}

type stageKeys struct {
	stage *service.SEOStage
	id    string
	// codes are the codes in each language, the same but for a few gachas
	codes []searchKey
	// zone are the keys of the zone of the stage, nil if it has none
	zone *zoneKeys
}

type zoneKeys struct {
	id    string
	names []searchKey
}

// searchIndex returns the search index of c, made once per catalog: SEOData replaces its catalog rather than change
// it.
func (t *tools) searchIndex(c *service.SEOCatalog) *searchIndex {
	if ix := t.index.Load(); ix != nil && ix.catalog == c {
		return ix
	}
	ix := newSearchIndex(c)
	t.index.Store(ix)
	return ix
}

func newSearchIndex(c *service.SEOCatalog) *searchIndex {
	ix := &searchIndex{catalog: c}
	for _, item := range c.ItemList {
		if !service.IsListedItemType(item.Type) {
			ix.unlisted = append(ix.unlisted, newItemKeys(item))
			continue
		}
		ix.items = append(ix.items, newItemKeys(item))
	}

	zones := make(map[*service.SEOZone]*zoneKeys, len(c.Zones))
	for _, stage := range c.StageList {
		k := stageKeys{stage: stage, id: fold(stage.ArkID)}
		for _, l := range service.StatsLangs {
			k.codes = appendKey(k.codes, codeKey, stage.Code[l], byCode)
		}
		if z := stage.Zone; z != nil {
			if zones[z] == nil {
				zk := &zoneKeys{id: fold(z.ArkID)}
				for _, l := range service.StatsLangs {
					zk.names = appendKey(zk.names, nameKey, z.Name[l], byZone)
				}
				zones[z] = zk
			}
			k.zone = zones[z]
		}
		ix.stages = append(ix.stages, k)
	}
	return ix
}

// newItemKeys returns the keys of the item: its ID, its names, its aliases and their readings.
func newItemKeys(item *service.SEOItem) itemKeys {
	k := itemKeys{item: item, id: fold(item.ArkID)}
	for _, l := range service.StatsLangs {
		k.names = appendKey(k.names, nameKey, item.Name[l], byName)
	}
	for _, l := range service.StatsLangs {
		for _, a := range item.Alias[l] {
			k.aliases = appendKey(k.aliases, nameKey, a, byAlias)
		}
	}
	for _, l := range service.StatsLangs {
		by := byPinyin
		if l == "ja" {
			by = byRomaji
		}
		for _, p := range item.Pron[l] {
			// the syllables are separated by backticks: gu`yuan`yan is typed guyuanyan
			k.aliases = appendKey(k.aliases, nameKey, strings.ReplaceAll(p, "`", ""), by)
		}
	}
	return k
}

// itemHit is an item a query matched.
type itemHit struct {
	item *service.SEOItem
	hit
}

// match tells how q matches the item, the tier of the hit being, from the best: 0 its ID; then exact, prefix and
// substring matches, each of a name before one of an alias or reading (readings by prefix at most).
func (k *itemKeys) match(q query) (hit, bool) {
	if k.id == q.id {
		return hit{tier: 0, key: searchKey{text: k.item.ArkID, by: byID}, how: matchExact}, true
	}
	h := hit{tier: -1}
	if key, how := bestMatch(k.names, q.name); how != "" {
		h = hit{tier: 1 + 2*rank(how), key: key, how: how}
	}
	if key, how := bestMatch(k.aliases, q.name); how != "" && (h.tier < 0 || 2+2*rank(how) < h.tier) {
		h = hit{tier: 2 + 2*rank(how), key: key, how: how}
	}
	return h, h.tier >= 0
}

// searchItems returns the listed items q matches, in the order of matchItems.
func (ix *searchIndex) searchItems(q query, lang string) []itemHit {
	return matchItems(ix.items, q, lang)
}

// searchUnlisted returns the items of the types not listed q matches, in the order of matchItems.
func (ix *searchIndex) searchUnlisted(q query, lang string) []itemHit {
	return matchItems(ix.unlisted, q, lang)
}

// matchItems returns the items of keys q matches: the best hits first; among hits as good, the lowest rarity first (a
// family of items shares aliases, and its base tier is the likeliest meant), then the shortest name in lang, then by
// sort ID, the order of the item list of the frontend.
func matchItems(keys []itemKeys, q query, lang string) []itemHit {
	var hits []itemHit
	for i := range keys {
		if h, ok := keys[i].match(q); ok {
			hits = append(hits, itemHit{item: keys[i].item, hit: h})
		}
	}
	slices.SortStableFunc(hits, func(a, b itemHit) int {
		return cmp.Or(
			cmp.Compare(a.tier, b.tier),
			cmp.Compare(a.item.Rarity, b.item.Rarity),
			cmp.Compare(utf8.RuneCountInString(a.item.DisplayName(lang)), utf8.RuneCountInString(b.item.DisplayName(lang))),
			cmp.Compare(a.item.SortID, b.item.SortID),
		)
	})
	return hits
}

// typeNote tells what the items of a type the frontend does not list are; "" for a listed type.
func typeNote(itemType string) string {
	switch {
	case service.IsListedItemType(itemType):
		return ""
	case itemType == "ARKPLANNER":
		return "ARKPLANNER items are crafted in the workshop: no stage drops them."
	case itemType == "RECRUIT_TAG":
		return "RECRUIT_TAG items are recruitment tags: their only rates are those of the recruitment (stage recruit), per recruitment."
	}
	return itemType + " items are not ones Penguin Statistics lists drop rates for: few stages drop them, if any."
}

// stageHit is a stage a query matched.
type stageHit struct {
	stage *service.SEOStage
	// open tells whether the stage is open on the server of the search
	open bool
	hit
}

// match tells how q matches the stage, the tier of the hit being, from the best: 0 its ID; 1 to 3 its code, exact,
// by prefix or by substring; 4 to 6 the name of its zone, exact (or the zone ID), by prefix or by substring.
func (k *stageKeys) match(q query) (hit, bool) {
	if k.id == q.id {
		return hit{tier: 0, key: searchKey{text: k.stage.ArkID, by: byID}, how: matchExact}, true
	}
	if key, how := bestMatch(k.codes, q.code); how != "" {
		return hit{tier: 1 + rank(how), key: key, how: how}, true
	}
	if k.zone == nil {
		return hit{}, false
	}
	if k.zone.id == q.id {
		return hit{tier: 4, key: searchKey{text: k.stage.Zone.ArkID, by: byZone}, how: matchExact}, true
	}
	if key, how := bestMatch(k.zone.names, q.name); how != "" {
		return hit{tier: 4 + rank(how), key: key, how: how}, true
	}
	return hit{}, false
}

// searchStages returns the stages q matches, the best hits first, then the stages open on server at now (codes are
// not unique: the stages of an event share them with its rerun and the permanent stages it left), the standard
// stages before the tough ones of the same code, the original before the permanent one and the rerun, then by stage
// ID.
func (ix *searchIndex) searchStages(q query, server string, now time.Time) []stageHit {
	var hits []stageHit
	for i := range ix.stages {
		if h, ok := ix.stages[i].match(q); ok {
			s := ix.stages[i].stage
			hits = append(hits, stageHit{stage: s, open: stageOpen(s, server, now), hit: h})
		}
	}
	slices.SortStableFunc(hits, func(a, b stageHit) int {
		return cmp.Or(
			cmp.Compare(a.tier, b.tier),
			-compareBool(a.open, b.open),
			compareBool(isTough(a.stage), isTough(b.stage)),
			cmp.Compare(rankVariant(variantOf(a.stage)), rankVariant(variantOf(b.stage))),
		)
	})
	return hits
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// The variants of a stage
const (
	variantOriginal  = "original"
	variantPermanent = "permanent"
	variantRerun     = "rerun"
)

// variantOf tells whether the stage is the stage of a rerun of an event (an ID ending in _rep), a stage an event left
// open for good (_perm), or neither.
func variantOf(s *service.SEOStage) string {
	switch {
	case strings.HasSuffix(s.ArkID, "_rep"):
		return variantRerun
	case strings.HasSuffix(s.ArkID, "_perm"):
		return variantPermanent
	}
	return variantOriginal
}

func rankVariant(v string) int {
	return slices.Index([]string{variantOriginal, variantPermanent, variantRerun}, v)
}

// isTough tells whether the stage is the tough version of a stage of the main story, of the same code.
func isTough(s *service.SEOStage) bool {
	return strings.HasPrefix(s.ArkID, "tough_")
}
