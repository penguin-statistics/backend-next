package seo

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"

	"exusiai.dev/backend-next/internal/service"
)

// licenseURL is the license of the drop data (meta.footer.copyright of the frontend)
const licenseURL = "https://creativecommons.org/licenses/by-nc/4.0/"

const (
	// itemRowsShown caps the rows of an item page: most items drop in about 10 stages, but some in hundreds
	itemRowsShown = 50
	// stageRowsShown caps the rows of a stage page, which drops 29 items at most
	stageRowsShown = 100
	// listLinksShown caps the links of a list page (CN: about 1,600 stages)
	listLinksShown = 3000
	// highlights is how many rows the description of a page quotes
	highlights = 3
	// highlightMinTimes is the number of samples a row needs to be quoted first in the description of an item
	highlightMinTimes = 1000
)

// validItemTypes are the types of the items the frontend lists (src/utils/getters.js)
var validItemTypes = []string{"MATERIAL", "CARD_EXP", "CHIP", "FURN", "ACTIVITY_ITEM"}

// zoneCategories are the zone categories in the order the stage list shows them
var zoneCategories = []string{"MAINLINE", "ACTIVITY_PERMANENT", "ACTIVITY", "WEEKLY", "GACHABOX"}

type pageKind string

const (
	kindItem      pageKind = "item"
	kindItemList  pageKind = "item_list"
	kindStage     pageKind = "stage"
	kindStageList pageKind = "stage_list"
	// kindOther is a path of the stats routes that is no page of its own: /{lang}/result[/{server}]
	kindOther pageKind = "other"
	kindNone  pageKind = "not_found"
)

// route is a parsed path of the stats routes.
type route struct {
	kind pageKind
	// lang is the language of the path, lowercased
	lang string
	// server is the server of the path, uppercased; "" if the path has none or one that is no server
	server  string
	itemID  string
	zoneID  string
	stageID string
}

// Page is everything a page is rendered from.
type Page struct {
	Kind pageKind
	// Outcome is the outcome the page is counted as
	Outcome string
	Status  int
	// Indexable tells whether search engines may index the page. A page that is not gets no canonical, hreflang and
	// structured data, and is sent with X-Robots-Tag.
	Indexable bool
	// Cacheable tells whether the page holds data, cached for the max-age of the pages; else for a minute
	Cacheable bool
	// Redirect is the canonical path a request is redirected to (301)
	Redirect     string
	LastModified time.Time

	Head Head
	Body Body
}

type Head struct {
	Lang        string
	Title       string
	Description string
	Robots      string
	Canonical   string
	Alternates  []Alternate
	SiteName    string
	OGLocale    string
	OGLocales   []string
	ImageAlt    string
	// JSONLD is marshaled into a JSON-LD script; nil for none
	JSONLD any
}

type Alternate struct {
	Hreflang string
	Href     string
}

type Link struct {
	Name string
	Href string
}

type ServerLink struct {
	Link
	Current bool
}

type Body struct {
	Lang            string
	BreadcrumbLabel string
	Breadcrumbs     []Link
	Subheading      string
	Heading         string
	ServerLabel     string
	Servers         []ServerLink
	Paragraphs      []string
	Table           *Table
	Notes           *Notes
	Groups          []Group
	Related         *Group
	Back            *Link
}

type Table struct {
	Caption string
	Headers []string
	Rows    []Row
}

type Row struct {
	Head      Link
	HeadExtra string
	Cells     []string
	Window    Window
}

// Window is a time range in the words of the frontend (stats.timeRange.*): Before Start Mid [End] After
type Window struct {
	Before string
	Start  TimeText
	Mid    string
	End    *TimeText
	After  string
}

type TimeText struct {
	Datetime string
	Text     string
}

type Notes struct {
	Definitions  string
	Stop         string
	UpdatedLabel string
	Updated      *TimeText
	Data         *Link
	LicenseLabel string
	LicenseURL   string
}

type Group struct {
	Heading   string
	Links     []Link
	Subgroups []Group
}

// builder builds the pages of a request.
type builder struct {
	site    string
	t       *texts
	r       route
	catalog *service.SEOCatalog
	// matrix is nil while the matrix of the server is not calculated
	matrix *service.SEOMatrix
}

func (b *builder) loc() *time.Location {
	if loc, ok := constant.LocMap[b.r.server]; ok {
		return loc
	}
	return time.UTC
}

// path is the canonical path of a page of the server of the request in lang
func (b *builder) path(lang string, kind pageKind, ids ...string) string {
	return pagePath(lang, b.r.server, kind, ids...)
}

func pagePath(lang, server string, kind pageKind, ids ...string) string {
	p := "/" + lang + "/result/" + strings.ToLower(server)
	switch kind {
	case kindItem, kindItemList:
		p += "/item"
	case kindStage, kindStageList:
		p += "/stage"
	}
	for _, id := range ids {
		p += "/" + url.PathEscape(id)
	}
	return p
}

func (b *builder) abs(path string) string {
	return b.site + path
}

func (b *builder) serverName(server string) string {
	return b.t.Servers[server]
}

func isValidItemType(t string) bool {
	for _, v := range validItemTypes {
		if v == t {
			return true
		}
	}
	return false
}

// itemOnServer tells whether the item has a page on the server: an item the frontend lists that exists there or
// has drops there
func (b *builder) itemOnServer(item *service.SEOItem) bool {
	if item == nil || !isValidItemType(item.Type) {
		return false
	}
	return item.Existence.In(b.r.server) || (b.matrix != nil && len(b.matrix.ByItem[item.ArkID]) > 0)
}

func (b *builder) stageOnServer(stage *service.SEOStage) bool {
	if stage == nil || stage.Zone == nil {
		return false
	}
	return stage.Existence.In(b.r.server) || (b.matrix != nil && len(b.matrix.ByStage[stage.ArkID]) > 0)
}

// isRecruit tells whether the stage is the recruitment statistics, which the frontend shows in beta with measures
// of its own
func isRecruit(stage *service.SEOStage) bool {
	return stage.ArkID == "recruit" || (stage.Zone != nil && stage.Zone.Category == "RECRUIT")
}

func (b *builder) itemName(item *service.SEOItem) string {
	if n := item.Name.In(b.t.Lang); n != "" {
		return n
	}
	return item.ArkID
}

func (b *builder) stageCode(stage *service.SEOStage) string {
	if c := stage.Code.In(b.t.Lang); c != "" {
		return c
	}
	return stage.ArkID
}

func (b *builder) zoneName(zone *service.SEOZone) string {
	if zone == nil {
		return ""
	}
	if n := zone.Name.In(b.t.Lang); n != "" {
		return n
	}
	return zone.ArkID
}

// stageName is the code of the stage and the name of its zone: stages have no name of their own. A stage named like
// its zone (the recruitment statistics) is named once.
func (b *builder) stageName(stage *service.SEOStage) string {
	code, zone := b.stageCode(stage), b.zoneName(stage.Zone)
	if zone == "" || zone == code {
		return code
	}
	return code + " " + zone
}

func (b *builder) stageLink(stageID string) Link {
	stage := b.catalog.Stages[stageID]
	if stage == nil {
		return Link{Name: stageID}
	}
	l := Link{Name: b.stageCode(stage)}
	if stage.Zone != nil {
		l.Href = b.abs(b.path(b.t.Lang, kindStage, stage.Zone.ArkID, stage.ArkID))
	}
	return l
}

func (b *builder) itemLink(itemID string) Link {
	item := b.catalog.Items[itemID]
	if item == nil {
		return Link{Name: itemID}
	}
	l := Link{Name: b.itemName(item)}
	if b.itemOnServer(item) {
		l.Href = b.abs(b.path(b.t.Lang, kindItem, item.ArkID))
	}
	return l
}

func (b *builder) title(t string) string {
	return t + " | " + b.t.SiteName
}

func (b *builder) listCrumb(kind pageKind) Link {
	list := b.t.ByItem
	if kind == kindStageList {
		list = b.t.ByStage
	}
	return Link{Name: fill(b.t.ListCrumb, "list", list, "server", b.serverName(b.r.server)), Href: b.abs(b.path(b.t.Lang, kind))}
}

func (b *builder) home() Link {
	return Link{Name: b.t.SiteName, Href: b.site + "/"}
}

// base is the page of the request with what all pages have: indexable unless made otherwise
func (b *builder) base(kind pageKind, title, imageAlt, description string, ids ...string) *Page {
	p := &Page{Kind: kind, Outcome: "ok", Status: 200, Indexable: true, Cacheable: b.matrix != nil}
	if b.matrix != nil {
		// and the catalog's, for a name changed or a stage moved to another zone
		p.LastModified = b.matrix.LastModified
		if b.catalog.ChangedAt.After(p.LastModified) {
			p.LastModified = b.catalog.ChangedAt
		}
	}
	p.Head = Head{
		Lang:        b.t.Lang,
		Title:       title,
		Description: description,
		Robots:      "index,follow",
		Canonical:   b.abs(b.path(b.t.Lang, kind, ids...)),
		SiteName:    b.t.SiteName,
		OGLocale:    b.t.OGLocale,
		ImageAlt:    imageAlt,
	}
	for _, l := range langs {
		p.Head.Alternates = append(p.Head.Alternates, Alternate{Hreflang: l, Href: b.abs(b.path(l, kind, ids...))})
		if l != b.t.Lang {
			p.Head.OGLocales = append(p.Head.OGLocales, textsByLang[l].OGLocale)
		}
	}
	p.Head.Alternates = append(p.Head.Alternates, Alternate{Hreflang: "x-default", Href: b.abs(b.path(defaultLang, kind, ids...))})
	p.Body = Body{Lang: b.t.Lang, BreadcrumbLabel: b.t.BreadcrumbLabel, ServerLabel: b.t.ServerLabel}
	return p
}

// noindex makes p a page search engines do not index
func noindex(p *Page, outcome string) {
	p.Indexable = false
	p.Outcome = outcome
	p.Head.Robots = "noindex,follow"
	p.Head.Canonical = ""
	p.Head.Alternates = nil
	p.Head.JSONLD = nil
}

// cold makes p, the page of an item or a stage, the page served while the matrix is not calculated, as after a
// restart: a 503 that search engines retry later instead of indexing the page without its data. Browsers show the
// app as usual.
func cold(p *Page) {
	noindex(p, "cold")
	p.Status = http.StatusServiceUnavailable
}

// servers are the links to the page on each server it is on
func (b *builder) servers(kind pageKind, on func(server string) bool, ids ...string) []ServerLink {
	var links []ServerLink
	for _, s := range constant.Servers {
		if s != b.r.server && !on(s) {
			continue
		}
		links = append(links, ServerLink{
			Link:    Link{Name: b.serverName(s), Href: b.abs(pagePath(b.t.Lang, s, kind, ids...))},
			Current: s == b.r.server,
		})
	}
	return links
}

func (b *builder) notFound() *Page {
	p := &Page{Kind: kindNone, Outcome: "not_found", Status: 404}
	p.Head = Head{
		Lang:        b.t.Lang,
		Title:       b.title(b.t.NotFoundTitle),
		Description: b.t.NotFound,
		Robots:      "noindex",
		SiteName:    b.t.SiteName,
		OGLocale:    b.t.OGLocale,
		ImageAlt:    b.t.NotFoundTitle,
	}
	p.Body = Body{Lang: b.t.Lang, Heading: b.t.NotFoundTitle, Paragraphs: []string{b.t.NotFound}}
	server := b.r.server
	if server == "" {
		server = "CN"
	}
	list := kindItemList
	if b.r.kind == kindStage || b.r.kind == kindStageList {
		list = kindStageList
	}
	back := b.t.ByItem
	if list == kindStageList {
		back = b.t.ByStage
	}
	p.Body.Back = &Link{
		Name: fill(b.t.ListCrumb, "list", back, "server", b.t.Servers[server]),
		Href: b.abs(pagePath(b.t.Lang, server, list)),
	}
	return p
}

func formatPercent(rate float64) string {
	return strconv.FormatFloat(rate*100, 'f', 2, 64) + "%"
}

// formatCount groups the digits of n by thousands
func formatCount(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + formatCount(-n)
	}
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// apPPR is the sanity required per item: sanity / rate (stats.headers.apPPR)
func apPPR(sanity int64, hasSanity bool, rate float64) string {
	if !hasSanity || rate == 0 {
		return "—"
	}
	return strconv.FormatFloat(float64(sanity)/rate, 'f', 2, 64)
}

func (b *builder) timeText(ms int64) TimeText {
	t := time.UnixMilli(ms).In(b.loc())
	return TimeText{Datetime: t.Format(time.RFC3339), Text: t.Format("2006-01-02")}
}

func (b *builder) window(row service.SEORow) Window {
	start := b.timeText(row.StartTime)
	if !row.EndTime.Valid {
		before, after, _ := strings.Cut(b.t.ToPresent, "{date}")
		return Window{Before: before, Start: start, Mid: after}
	}
	end := b.timeText(row.EndTime.Int64)
	before, rest, _ := strings.Cut(b.t.InBetween, "{0}")
	mid, after, _ := strings.Cut(rest, "{1}")
	return Window{Before: before, Start: start, Mid: mid, End: &end, After: after}
}

func (w Window) text() string {
	s := w.Before + w.Start.Text + w.Mid
	if w.End != nil {
		s += w.End.Text
	}
	return s + w.After
}

func (b *builder) updated() *TimeText {
	if b.matrix == nil || b.matrix.LastModified.IsZero() {
		return nil
	}
	t := b.matrix.LastModified.In(b.loc())
	return &TimeText{Datetime: t.Format(time.RFC3339), Text: t.Format("2006-01-02 15:04") + " (" + t.Format("MST") + ")"}
}

func (b *builder) updatedDesc() string {
	if u := b.updated(); u != nil {
		return fill(b.t.UpdatedDesc, "date", u.Text[:len("2006-01-02")])
	}
	return ""
}

func (b *builder) dataURL(filter, id string) string {
	return b.site + "/PenguinStats/api/v2/result/matrix?server=" + b.r.server + "&" + filter + "=" + url.QueryEscape(id) + "&show_closed_zones=true"
}

func (b *builder) notes(filter, id string) *Notes {
	n := &Notes{
		Definitions:  b.t.Definitions,
		Stop:         b.t.Stop,
		UpdatedLabel: b.t.Updated,
		Updated:      b.updated(),
		LicenseLabel: b.t.License,
		LicenseURL:   licenseURL,
	}
	if filter != "" {
		n.Data = &Link{Name: b.t.FullData, Href: b.dataURL(filter, id)}
	}
	return n
}

func (b *builder) itemPage() *Page {
	item := b.catalog.Items[b.r.itemID]
	if !b.itemOnServer(item) {
		return b.notFound()
	}
	name := b.itemName(item)
	server := b.serverName(b.r.server)
	heading := fill(b.t.ItemTitle, "name", name, "server", server)

	var rows []service.SEORow
	if b.matrix != nil {
		rows = b.matrix.ByItem[item.ArkID]
	}
	desc := fill(b.t.ItemDescNoData, "name", name, "server", server) + b.t.Tagline
	if len(rows) > 0 {
		desc = fill(b.t.ItemDesc, "name", name, "server", server, "highlights", b.itemHighlights(rows), "n", formatCount(len(rows))) +
			b.updatedDesc() + b.t.Tagline
	}

	p := b.base(kindItem, b.title(heading), heading, desc, item.ArkID)
	p.Body.Breadcrumbs = []Link{b.home(), b.listCrumb(kindItemList), {Name: name}}
	p.Body.Heading = fill(b.t.ItemHeading, "item", name)
	p.Body.Servers = b.servers(kindItem, func(s string) bool { return item.Existence.In(s) }, item.ArkID)
	p.Body.Related = b.related(item)

	switch {
	case b.matrix == nil:
		p.Body.Paragraphs = []string{b.t.Cold}
		cold(p)
	case len(rows) == 0:
		p.Body.Paragraphs = []string{b.t.Empty}
		noindex(p, "no_data")
	default:
		shown := rows
		if len(shown) > itemRowsShown {
			shown = shown[:itemRowsShown]
		}
		table := &Table{
			Caption: fill(b.t.ItemCaption, "name", name, "server", server, "shown", strconv.Itoa(len(shown)), "total", strconv.Itoa(len(rows))),
			Headers: []string{b.t.HStage, b.t.HQuantity, b.t.HTimes, b.t.HPercentage, b.t.HAPPPR, b.t.HAPCost, b.t.HTimeRange},
		}
		for _, row := range shown {
			r := Row{Head: b.stageLink(row.StageID), Window: b.window(row)}
			sanity, hasSanity := int64(0), false
			if stage := b.catalog.Stages[row.StageID]; stage != nil {
				r.HeadExtra = b.zoneName(stage.Zone)
				sanity, hasSanity = stage.Sanity.Int64, stage.Sanity.Valid
			}
			cost := "—"
			if hasSanity {
				cost = strconv.FormatInt(sanity, 10)
			}
			r.Cells = []string{strconv.Itoa(row.Quantity), strconv.Itoa(row.Times), formatPercent(row.Rate), apPPR(sanity, hasSanity, row.Rate), cost}
			table.Rows = append(table.Rows, r)
		}
		p.Body.Table = table
		p.Body.Notes = b.notes("itemFilter", item.ArkID)
		p.Head.JSONLD = b.jsonLD(p, fill(b.t.DatasetName, "name", name, "server", server), "itemFilter", item.ArkID, rows, []Link{b.home(), b.listCrumb(kindItemList), {Name: name}})
	}
	return p
}

// itemHighlights quotes the rows with the highest rates among those still open with many samples, then the rows
// with the most samples: the highest rates of the others are often those of a handful of runs
func (b *builder) itemHighlights(rows []service.SEORow) string {
	var picked, rest []service.SEORow
	for _, row := range rows {
		if len(picked) < highlights && !row.EndTime.Valid && row.Times >= highlightMinTimes {
			picked = append(picked, row)
		} else {
			rest = append(rest, row)
		}
	}
	if n := highlights - len(picked); n > 0 {
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].Times > rest[j].Times })
		if n > len(rest) {
			n = len(rest)
		}
		picked = append(picked, rest[:n]...)
	}
	parts := make([]string, len(picked))
	for i, row := range picked {
		pattern := b.t.ItemHighlight
		if i == 0 {
			pattern = b.t.ItemHighlightFirst
		}
		parts[i] = fill(pattern, "code", b.stageLink(row.StageID).Name, "p", formatPercent(row.Rate), "n", formatCount(row.Times))
	}
	return strings.Join(parts, b.t.HighlightSep)
}

func (b *builder) related(item *service.SEOItem) *Group {
	if item.Group == "" {
		return nil
	}
	g := &Group{Heading: b.t.Related}
	for _, other := range b.catalog.ItemList {
		if other != item && other.Group == item.Group && b.itemOnServer(other) {
			g.Links = append(g.Links, Link{Name: b.itemName(other), Href: b.abs(b.path(b.t.Lang, kindItem, other.ArkID))})
		}
	}
	if len(g.Links) == 0 {
		return nil
	}
	return g
}

func (b *builder) stagePage() *Page {
	stage := b.catalog.Stages[b.r.stageID]
	if !b.stageOnServer(stage) {
		return b.notFound()
	}
	if stage.Zone.ArkID != b.r.zoneID {
		// the zone is part of the path, the stage alone tells the page
		return &Page{Kind: kindStage, Outcome: "redirect", Redirect: b.path(b.t.Lang, kindStage, stage.Zone.ArkID, stage.ArkID)}
	}
	name := b.stageName(stage)
	code := b.stageCode(stage)
	server := b.serverName(b.r.server)
	heading := fill(b.t.StageTitle, "stage", name, "server", server)

	var rows []service.SEORow
	if b.matrix != nil {
		rows = b.matrix.ByStage[stage.ArkID]
	}
	desc := fill(b.t.StageDescNoData, "stage", name, "server", server) + b.t.Tagline
	if len(rows) > 0 {
		desc = fill(b.t.StageDesc, "stage", name, "server", server, "highlights", b.stageHighlights(rows),
			"n", formatCount(rows[0].Times), "window", b.window(rows[0]).text())
		if stage.Sanity.Valid {
			desc += fill(b.t.SanityDesc, "sanity", strconv.FormatInt(stage.Sanity.Int64, 10))
		}
		desc += b.updatedDesc() + b.t.Tagline
	}

	p := b.base(kindStage, b.title(heading), heading, desc, stage.Zone.ArkID, stage.ArkID)
	p.Body.Breadcrumbs = []Link{b.home(), b.listCrumb(kindStageList), {Name: name}}
	p.Body.Subheading = b.zoneName(stage.Zone)
	p.Body.Heading = fill(b.t.StageHeading, "stage", code)
	p.Body.Servers = b.servers(kindStage, func(s string) bool { return stage.Existence.In(s) }, stage.Zone.ArkID, stage.ArkID)
	if stage.Sanity.Valid {
		p.Body.Paragraphs = []string{fill(b.t.SanityFact, "sanity", strconv.FormatInt(stage.Sanity.Int64, 10))}
	}

	switch {
	case b.matrix == nil:
		p.Body.Paragraphs = append(p.Body.Paragraphs, b.t.Cold)
		cold(p)
	case len(rows) == 0:
		p.Body.Paragraphs = append(p.Body.Paragraphs, b.t.Empty)
		noindex(p, "no_data")
	default:
		shown := rows
		if len(shown) > stageRowsShown {
			shown = shown[:stageRowsShown]
		}
		table := &Table{
			Caption: fill(b.t.StageCaption, "stage", name, "server", server),
			Headers: []string{b.t.HItem, b.t.HQuantity, b.t.HTimes, b.t.HPercentage, b.t.HAPPPR, b.t.HTimeRange},
		}
		for _, row := range shown {
			table.Rows = append(table.Rows, Row{
				Head:   b.itemLink(row.ItemID),
				Cells:  []string{strconv.Itoa(row.Quantity), strconv.Itoa(row.Times), formatPercent(row.Rate), apPPR(stage.Sanity.Int64, stage.Sanity.Valid, row.Rate)},
				Window: b.window(row),
			})
		}
		p.Body.Table = table
		p.Body.Notes = b.notes("stageFilter", stage.ArkID)
		p.Head.JSONLD = b.jsonLD(p, fill(b.t.DatasetName, "name", name, "server", server), "stageFilter", stage.ArkID, rows, []Link{b.home(), b.listCrumb(kindStageList), {Name: name}})
	}
	if isRecruit(stage) && p.Indexable {
		// the recruitment statistics are in beta in the frontend, with measures of their own
		noindex(p, "noindex")
	}
	return p
}

func (b *builder) stageHighlights(rows []service.SEORow) string {
	n := len(rows)
	if n > highlights {
		n = highlights
	}
	parts := make([]string, n)
	for i, row := range rows[:n] {
		pattern := b.t.StageHighlight
		if i == 0 {
			pattern = b.t.StageHighlightFirst
		}
		parts[i] = fill(pattern, "item", b.itemLink(row.ItemID).Name, "p", formatPercent(row.Rate))
	}
	return strings.Join(parts, b.t.HighlightSep)
}

// listedItems are the items the item list links: those with drops on the server, or, while the matrix is not
// calculated, those existing there
func (b *builder) listedItems() []*service.SEOItem {
	var items []*service.SEOItem
	for _, item := range b.catalog.ItemList {
		if !isValidItemType(item.Type) {
			continue
		}
		if b.matrix != nil && len(b.matrix.ByItem[item.ArkID]) == 0 || b.matrix == nil && !item.Existence.In(b.r.server) {
			continue
		}
		items = append(items, item)
	}
	return items
}

// listedStages are the stages the stage list links, like listedItems
func (b *builder) listedStages() []*service.SEOStage {
	var stages []*service.SEOStage
	for _, stage := range b.catalog.StageList {
		if stage.Zone == nil || isRecruit(stage) {
			continue
		}
		if b.matrix != nil && len(b.matrix.ByStage[stage.ArkID]) == 0 || b.matrix == nil && !stage.Existence.In(b.r.server) {
			continue
		}
		stages = append(stages, stage)
	}
	return stages
}

func (b *builder) listDesc(pattern string, items, stages int) string {
	return fill(pattern, "server", b.serverName(b.r.server), "items", formatCount(items), "stages", formatCount(stages)) + b.updatedDesc() + b.t.Tagline
}

func (b *builder) itemListPage() *Page {
	items := b.listedItems()
	heading := fill(b.t.ItemListTitle, "server", b.serverName(b.r.server))
	p := b.base(kindItemList, b.title(heading), heading, b.listDesc(b.t.ItemListDesc, len(items), len(b.listedStages())))
	p.Body.Breadcrumbs = []Link{b.home(), {Name: b.listCrumb(kindItemList).Name}}
	p.Body.Heading = heading
	p.Body.Servers = b.servers(kindItemList, func(string) bool { return true })
	if b.matrix == nil {
		p.Body.Paragraphs = []string{b.t.Cold}
	}
	byType := map[string]*Group{}
	for _, t := range validItemTypes {
		byType[t] = &Group{Heading: b.t.ItemCategories[t]}
	}
	for i, item := range items {
		if i == listLinksShown {
			break
		}
		g := byType[item.Type]
		g.Links = append(g.Links, Link{Name: b.itemName(item), Href: b.abs(b.path(b.t.Lang, kindItem, item.ArkID))})
	}
	for _, t := range validItemTypes {
		if len(byType[t].Links) > 0 {
			p.Body.Groups = append(p.Body.Groups, *byType[t])
		}
	}
	p.Head.JSONLD = b.breadcrumbLD(p.Body.Breadcrumbs)
	return p
}

func (b *builder) stageListPage() *Page {
	stages := b.listedStages()
	heading := fill(b.t.StageListTitle, "server", b.serverName(b.r.server))
	p := b.base(kindStageList, b.title(heading), heading, b.listDesc(b.t.StageListDesc, len(b.listedItems()), len(stages)))
	p.Body.Breadcrumbs = []Link{b.home(), {Name: b.listCrumb(kindStageList).Name}}
	p.Body.Heading = heading
	p.Body.Servers = b.servers(kindStageList, func(string) bool { return true })
	if b.matrix == nil {
		p.Body.Paragraphs = []string{b.t.Cold}
	}

	byZone := map[*service.SEOZone][]*service.SEOStage{}
	var zones []*service.SEOZone
	for i, stage := range stages {
		if i == listLinksShown {
			break
		}
		if _, ok := byZone[stage.Zone]; !ok {
			zones = append(zones, stage.Zone)
		}
		byZone[stage.Zone] = append(byZone[stage.Zone], stage)
	}
	server := b.r.server
	sort.SliceStable(zones, func(i, j int) bool {
		a, c := zones[i], zones[j]
		if ca, cc := categoryOrder(a.Category), categoryOrder(c.Category); ca != cc {
			return ca < cc
		}
		if a.Category == "ACTIVITY" {
			// latest events first
			if oa, oc := a.Existence[server].OpenTime.Int64, c.Existence[server].OpenTime.Int64; oa != oc {
				return oa > oc
			}
		}
		if a.Index != c.Index {
			return a.Index < c.Index
		}
		return a.ArkID < c.ArkID
	})
	var cur *Group
	for _, zone := range zones {
		heading := b.t.ZoneCategories[zone.Category]
		if heading == "" {
			heading = zone.Category
		}
		if cur == nil || cur.Heading != heading {
			p.Body.Groups = append(p.Body.Groups, Group{Heading: heading})
			cur = &p.Body.Groups[len(p.Body.Groups)-1]
		}
		sub := Group{Heading: b.zoneName(zone)}
		for _, stage := range byZone[zone] {
			sub.Links = append(sub.Links, Link{Name: b.stageCode(stage), Href: b.abs(b.path(b.t.Lang, kindStage, zone.ArkID, stage.ArkID))})
		}
		cur.Subgroups = append(cur.Subgroups, sub)
	}
	p.Head.JSONLD = b.breadcrumbLD(p.Body.Breadcrumbs)
	return p
}

func categoryOrder(category string) int {
	for i, c := range zoneCategories {
		if c == category {
			return i
		}
	}
	return len(zoneCategories)
}
