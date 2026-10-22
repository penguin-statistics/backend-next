package mcp

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

// What the results of the tools share.

// errCold is the error of a tool that needs data that is not loaded yet: the catalog for a few seconds after a start,
// the matrix of a server for up to a few minutes (see service.SEOData). The SDK sends its text as the result, an
// error (isError) that the model can act on; the metrics count it as cold. A structured result with the time to
// wait is not possible: a result with an output schema must match it, errors included.
var errCold = errors.New("Penguin Statistics is still loading its data (the server restarted recently). Retry in about 2 minutes.")

// catalog returns the catalog, or errCold before it is loaded.
func (t *tools) catalog() (*service.SEOCatalog, error) {
	if c := t.data.Catalog(); c != nil {
		return c, nil
	}
	return nil, errCold
}

// matrix returns the matrix of server, or errCold while it is not calculated.
func (t *tools) matrix(server string) (*service.SEOMatrix, error) {
	if m := t.data.Matrix(server); m != nil {
		return m, nil
	}
	return nil, errCold
}

// source credits the data of a result: CC BY-NC 4.0 requires it, so every structured result has one.
type source struct {
	Name        string `json:"name"`
	URL         string `json:"url" jsonschema:"The page of this data on Penguin Statistics, or its home page"`
	License     string `json:"license"`
	LicenseURL  string `json:"licenseUrl"`
	Attribution string `json:"attribution" jsonschema:"The credit to give wherever this data is used"`
}

// source returns the credit of a result made of the data on pageURL, the site's home page if "".
func (t *tools) source(pageURL string) source {
	if pageURL == "" {
		pageURL = t.site
	}
	return source{
		Name:        serverTitle,
		URL:         pageURL,
		License:     service.DropDataLicenseName,
		LicenseURL:  service.DropDataLicenseURL,
		Attribution: "Data: " + serverTitle + " (" + t.site + "), " + service.DropDataLicenseName,
	}
}

// line is the credit in the text content of a result, its last line.
func (s source) line() string {
	return s.Attribution + " (" + s.LicenseURL + "). Source: " + s.URL
}

// definitions define the numbers of the drop data.
type definitions struct {
	Times         string `json:"times"`
	Quantity      string `json:"quantity"`
	DropRate      string `json:"dropRate"`
	SanityPerItem string `json:"sanityPerItem"`
	Window        string `json:"window"`
}

var dropDefinitions = definitions{
	Times:    "Runs of the stage reported in the window (for a gacha: items drawn; for the recruitment: recruitments).",
	Quantity: "Total number of the item dropped in those runs.",
	DropRate: "quantity ÷ times: the expected number of the item per run, which exceeds 1 (100%) for items dropped " +
		"more than once per run.",
	SanityPerItem: "Sanity cost of a run ÷ dropRate: the expected sanity spent per item; null for stages without a " +
		"sanity cost (gachas, the recruitment).",
	Window: "start to end of the counts: since the drop table of the stage last changed for this item; end null " +
		"means still counting.",
}

// The arguments of the tools that choose a server or a language.

// servers are the game servers, the values of a server argument
var servers = constant.Servers

// serverOf returns the server argument, or the default server if none was given: the SDK fills in the default of
// the schema, but a handler called otherwise gets none.
func serverOf(server string) string {
	if server == "" {
		return constant.DefaultServer
	}
	return server
}

// langOf returns the language argument, or the default language if none was given (see serverOf).
func langOf(lang string) string {
	if lang == "" {
		return service.StatsDefaultLang
	}
	return lang
}

// limitOf returns a limit argument, def if none was given (see serverOf), at most maximum.
func limitOf(limit, def, maximum int) int {
	if limit <= 0 {
		return def
	}
	return min(limit, maximum)
}

// property adjusts a property of an input schema.
type property func(*jsonschema.Schema)

// inputSchema is the input schema of a tool: the one the SDK would infer from In, with the descriptions of its
// jsonschema tags, and the properties in adjust adjusted, as tags set only descriptions. A property with a default
// must be omitempty in In: the SDK fills in the defaults of the properties that are not required. It panics if In
// has no such property.
func inputSchema[In any](adjust map[string]property) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(err)
	}
	if s.Properties == nil {
		// "properties": {} for a tool without arguments, which some clients require of an object
		s.Properties = map[string]*jsonschema.Schema{}
	}
	for name, set := range adjust {
		p, ok := s.Properties[name]
		if !ok {
			panic("mcp: the input has no property " + name)
		}
		set(p)
	}
	return s
}

// serverProperty is a server argument, CN by default.
func serverProperty(p *jsonschema.Schema) {
	p.Description = "Game server: CN (China), US (global, English), JP (Japan) or KR (Korea). Default CN."
	p.Enum = make([]any, len(servers))
	for i, s := range servers {
		p.Enum[i] = s
	}
	p.Default = json.RawMessage(strconv.Quote(constant.DefaultServer))
}

// langProperty is a language argument, of the names in results only, en by default.
func langProperty(p *jsonschema.Schema) {
	p.Description = "Language of the names in the result: zh, en, ja or ko. Default en."
	p.Enum = make([]any, len(service.StatsLangs))
	for i, l := range service.StatsLangs {
		p.Enum[i] = l
	}
	p.Default = json.RawMessage(strconv.Quote(service.StatsDefaultLang))
}

// intProperty is an integer argument from minimum to maximum, def by default.
func intProperty(def, minimum, maximum int) property {
	return func(p *jsonschema.Schema) {
		p.Default = json.RawMessage(strconv.Itoa(def))
		p.Minimum, p.Maximum = jsonschema.Ptr(float64(minimum)), jsonschema.Ptr(float64(maximum))
	}
}

// lengthProperty is a string argument of minimum to maximum characters.
func lengthProperty(minimum, maximum int) property {
	return func(p *jsonschema.Schema) {
		p.MinLength, p.MaxLength = jsonschema.Ptr(minimum), jsonschema.Ptr(maximum)
	}
}

// Servers and pages.

// existsOn lists the servers e exists on, in the order of servers: CN first.
func existsOn(e service.SEOExistence) []string {
	on := make([]string, 0, len(servers))
	for _, s := range servers {
		if e.In(s) {
			on = append(on, s)
		}
	}
	return on
}

// link is the URL of a page in a result: null if there is no page (ok is false).
func link(u string, ok bool) *string {
	if !ok {
		return nil
	}
	return &u
}

// stageRef is a stage in a result, on a server.
type stageRef struct {
	StageID  string   `json:"stageId"`
	Code     string   `json:"code" jsonschema:"Code in the language asked for"`
	ZoneID   string   `json:"zoneId"`
	ZoneName string   `json:"zoneName" jsonschema:"Name of the zone in the language asked for"`
	ZoneType string   `json:"zoneType" jsonschema:"MAINLINE (the main story), ACTIVITY (an event), ACTIVITY_PERMANENT (a side story or intermezzo, open for good), WEEKLY (supplies and chips), GACHABOX or RECRUIT"`
	APCost   *int64   `json:"apCost" jsonschema:"Sanity cost of a run; null without one (gachas, the recruitment)"`
	OpenNow  bool     `json:"openNow" jsonschema:"Whether the stage is open on the server now, by the open and close times of the stage and its zone; supply and chip stages (zoneType WEEKLY) count as open every day, as their weekday rotation is not tracked"`
	ExistsOn []string `json:"existsOn" jsonschema:"Game servers the stage exists on"`
	PageURL  *string  `json:"pageUrl" jsonschema:"Page of the drops of the stage on Penguin Statistics, on the server or else the first server it exists on; null if there is none"`
}

// stageRef is the stage on server at now, in lang. Its page is on server, or else on the first server it exists on. m
// is the matrix of server, nil while it is not calculated (the page then follows the existence only).
func (t *tools) stageRef(stage *service.SEOStage, server, lang string, m *service.SEOMatrix, now time.Time) stageRef {
	ref := stageRef{
		StageID:  stage.ArkID,
		Code:     stage.DisplayCode(lang),
		ZoneName: stage.Zone.DisplayName(lang),
		OpenNow:  stageOpen(stage, server, now),
		ExistsOn: existsOn(stage.Existence),
	}
	if stage.Zone != nil {
		ref.ZoneID, ref.ZoneType = stage.Zone.ArkID, stage.Zone.Category
	}
	if sanity, ok := stage.SanityCost(); ok {
		ref.APCost = &sanity
	}
	switch {
	case service.StageOnServer(stage, server, m):
		ref.PageURL = link(service.StagePageURL(t.site, lang, server, stage))
	case len(ref.ExistsOn) > 0:
		ref.PageURL = link(service.StagePageURL(t.site, lang, ref.ExistsOn[0], stage))
	}
	return ref
}

// Numbers and times.

// now is the time of a call.
func (t *tools) now() time.Time {
	if t.clock != nil {
		return t.clock()
	}
	return time.Now()
}

// stageOpen tells whether the stage is open on server at now: the stage and its zone, if it has one (see
// service.SEOExistence.OpenAt).
func stageOpen(stage *service.SEOStage, server string, now time.Time) bool {
	return stage.Existence.OpenAt(server, now) && (stage.Zone == nil || stage.Zone.Existence.OpenAt(server, now))
}

// rotates tells whether the stage is a supply or chip stage (zone category WEEKLY): those open on some weekdays only, a
// rotation the data does not have, so stageOpen counts them open every day
func rotates(stage *service.SEOStage) bool {
	return stage != nil && stage.Zone != nil && stage.Zone.Category == constant.ZoneCategoryWeekly
}

// rotationCaveat qualifies openNow wherever a supply or chip stage is shown open (see rotates)
const rotationCaveat = "Supply and chip stages (zone type WEEKLY) open on some weekdays only, which is not tracked: they " +
	"count as open every day, so check today's schedule in the game."

// rounded returns x rounded to places decimals, or nil (null) if ok is false or x is not finite: encoding/json
// refuses NaN and ±Inf, and one such number would turn the whole result into a protocol error.
func rounded(x float64, ok bool, places int) *float64 {
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	p := math.Pow10(places)
	r := math.Round(x*p) / p
	return &r
}

// utcTime formats epoch milliseconds as RFC 3339 in UTC: models read it better than a number.
func utcTime(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// dataAsOf is when the matrix was calculated, RFC 3339 in UTC; "" if unknown or there is no matrix.
func dataAsOf(m *service.SEOMatrix) string {
	if m == nil || m.LastModified.IsZero() {
		return ""
	}
	return m.LastModified.UTC().Format(time.RFC3339)
}

// The text content of the results.

// textResult is a result whose text content is the lines. Every tool sets its text content: else the SDK sends the
// structured content again as text, doubling the size of the result. Clients that do not read the structured
// content get the text only, so it has the key numbers (see table), and ends with the credit (see source.line).
func textResult(lines ...string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: strings.Join(lines, "\n")}}}
}

// table is a Markdown table of the rows under the header.
func table(header []string, rows [][]string) string {
	var b strings.Builder
	row := func(cells []string) {
		b.WriteString("|")
		for _, c := range cells {
			b.WriteString(" ")
			b.WriteString(cell(c))
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}
	row(header)
	b.WriteString("|")
	for range header {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, r := range rows {
		row(r)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

var cellReplacer = strings.NewReplacer("|", `\|`, "\r\n", " ", "\n", " ", "\r", " ")

// cell is s as a cell of a Markdown table: on one line, its pipes escaped, "-" if empty
func cell(s string) string {
	if s == "" {
		return "-"
	}
	return cellReplacer.Replace(s)
}

// number formats x in the text content, with no more digits than it has.
func number(x float64) string {
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// yesNo is a boolean in the text content.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
