package mcp

import (
	"context"

	"exusiai.dev/gommon/constant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

// The public API of Penguin Statistics, which the tools are a part of
const (
	apiDocsURL = "https://developer.penguin-stats.io"
	// openAPIPath is the Swagger document of the API (controller/meta.RegisterSwagger, routed by the ingress)
	openAPIPath = "/swagger/doc.json"
)

const datasetDescription = "Crowdsourced Arknights drop statistics: players report the items each run of a stage " +
	"dropped (by hand or by recognition of the result screen), and Penguin Statistics adds them up per game server " +
	"into drop rates by stage and item."

// datasetCaveats are what to keep in mind when reading the drop data.
var datasetCaveats = []string{
	"Crowdsourced: the counts come from the reports of players, not from the game; rates of few runs (times) are noisy.",
	"Penguin Statistics may add the stages and events of the global servers (US, JP, KR) late, or not yet: latestEventOpenTime is the newest event it lists for each server, and a newer one may be running in the game.",
	"The counts of each stage and item start when the drop table of the stage last changed for that item, so start and times can differ between the items of a stage.",
	"Items never dropped by a stage (quantity 0) are left out: a missing item may still be in its drop table.",
	"Gacha and recruitment rows count items drawn and recruitments, not runs, and have no sanity cost.",
	"The weekday rotation of the supply stages is not modelled: they count as open every day.",
}

// meta#20 also lists a snapshot URL: it waits for the bulk snapshots of meta#19; docs and openapi point to the public
// API meanwhile.
type datasetInfo struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Website     string       `json:"website"`
	Docs        string       `json:"docs" jsonschema:"Documentation of the public API of Penguin Statistics"`
	OpenAPI     string       `json:"openapi" jsonschema:"OpenAPI (Swagger 2.0) document of the public API"`
	License     string       `json:"license"`
	LicenseURL  string       `json:"licenseUrl"`
	Servers     []serverData `json:"servers"`
	Definitions definitions  `json:"definitions"`
	Caveats     []string     `json:"caveats"`
	Source      source       `json:"source"`
}

type serverData struct {
	Server   string `json:"server"`
	Timezone string `json:"timezone" jsonschema:"Time zone of the days of the server, e.g. UTC+8"`
	// a matrix is calculated in up to a few minutes after a start
	MatrixAvailable bool   `json:"matrixAvailable" jsonschema:"Whether the drop data of the server is loaded"`
	DataAsOf        string `json:"dataAsOf,omitempty" jsonschema:"When the drop counts of the server were last calculated, RFC 3339 in UTC; not how recent its stages and events are (see latestEventOpenTime)"`
	// the catalog has no time of its own: the newest event of a server tells how far it is kept up with the game
	LatestEventOpenTime string `json:"latestEventOpenTime,omitempty" jsonschema:"Open time of the newest event Penguin Statistics lists for the server, RFC 3339 in UTC: a newer event running in the game may be missing from its catalog"`
}

// latestEventOpenTime is the open time of the newest event of c on server (a zone of the category ACTIVITY that exists
// there), RFC 3339 in UTC; "" if there is none or no catalog yet.
func latestEventOpenTime(c *service.SEOCatalog, server string) string {
	if c == nil {
		return ""
	}
	var latest int64
	for _, z := range c.Zones {
		if z.Category == constant.ZoneCategoryActivity && z.Existence.In(server) {
			latest = max(latest, z.Existence[server].OpenTime.Int64)
		}
	}
	if latest <= 0 {
		return ""
	}
	return utcTime(latest)
}

func getDatasetInfoTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "get_dataset_info",
		Title: "Dataset info",
		Description: "Describes the Penguin Statistics drop data: what its numbers mean (times, quantity, drop rate, " +
			"sanity per item, window), the game servers with their time zones and how fresh the data of each is, " +
			"the caveats, the license and how to credit it. Call it when unsure how to read or cite the numbers.",
		InputSchema: inputSchema[struct{}](nil),
	}
}

func (t *tools) getDatasetInfo(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, datasetInfo, error) {
	src := t.source("")
	out := datasetInfo{
		Name:        serverTitle,
		Description: datasetDescription,
		Website:     t.site,
		Docs:        apiDocsURL,
		OpenAPI:     t.site + openAPIPath,
		License:     src.License,
		LicenseURL:  src.LicenseURL,
		Servers:     make([]serverData, 0, len(servers)),
		Definitions: dropDefinitions,
		Caveats:     datasetCaveats,
		Source:      src,
	}
	rows := make([][]string, 0, len(servers))
	// the info answers cold too: what is not loaded is left out
	c := t.data.Catalog()
	for _, s := range servers {
		m := t.data.Matrix(s)
		d := serverData{
			Server:              s,
			Timezone:            constant.LocMap[s].String(),
			MatrixAvailable:     m != nil,
			DataAsOf:            dataAsOf(m),
			LatestEventOpenTime: latestEventOpenTime(c, s),
		}
		out.Servers = append(out.Servers, d)
		status := "loaded"
		if !d.MatrixAvailable {
			status = "loading (retry in about 2 minutes)"
		}
		rows = append(rows, []string{s, d.Timezone, status, d.DataAsOf, d.LatestEventOpenTime})
	}

	lines := []string{
		serverTitle + " (" + t.site + "): " + datasetDescription,
		"",
		table([]string{"server", "time zone", "drop data", "data as of (UTC)", "newest event open time (UTC)"}, rows),
		"",
		"Definitions:",
		"- times: " + dropDefinitions.Times,
		"- quantity: " + dropDefinitions.Quantity,
		"- dropRate: " + dropDefinitions.DropRate,
		"- sanityPerItem: " + dropDefinitions.SanityPerItem,
		"- window: " + dropDefinitions.Window,
		"",
		"Caveats:",
	}
	for _, c := range datasetCaveats {
		lines = append(lines, "- "+c)
	}
	lines = append(lines, "", "API: "+apiDocsURL+" (OpenAPI: "+out.OpenAPI+")", src.line())
	return textResult(lines...), out, nil
}
