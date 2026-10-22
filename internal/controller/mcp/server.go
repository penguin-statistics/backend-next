// Package mcp serves a read-only MCP server (https://modelcontextprotocol.io) of the drop data at /mcp, for AI
// assistants: stateless Streamable HTTP with JSON responses, no authentication and no writes. Its tools are made of
// the catalog and the matrices of the stats pages (service.SEOData), which are in memory: no request reaches the
// database.
//
// Each tool has a file of its own (tool_<name>.go) and a line in tools.register; output.go has what their results
// share: the credit of the data, the definitions, the server and language arguments, the text content; search.go
// finds the items and stages by what users and models call them.
package mcp

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

// Data is the data the tools are made of.
type Data interface {
	// Catalog returns the items, stages and zones, or nil before they are loaded
	Catalog() *service.SEOCatalog
	// Matrix returns the matrix of a server, or nil while it is not calculated
	Matrix(server string) *service.SEOMatrix
}

const (
	serverName  = "penguin-stats"
	serverTitle = "Penguin Statistics"
)

// instructions tell the model what the data is, how to use the tools and how to credit the data (600 characters at
// most, as clients may cut them)
const instructions = "Penguin Statistics (penguin-stats.io) publishes crowdsourced Arknights drop statistics: for each " +
	"game server (CN, US, JP, KR), how often each stage drops each item, from the reports of players. Find the IDs " +
	"of items and stages with search_items and search_stages (any language, aliases, pinyin, codes like 1-7). Call " +
	"get_dataset_info for what the numbers mean, how fresh the data of each server is and its caveats. When you use " +
	"these numbers, name the server, the data-as-of time and credit Penguin Statistics (penguin-stats.io), CC BY-NC 4.0."

// listTTL is how long clients may keep the list of the tools, which only a release changes
const listTTL = time.Hour

// tools holds what the tools are made of.
type tools struct {
	data Data
	// site is the origin of the stats pages the results link to, without a trailing slash
	site string
	// clock tells the time of a call, time.Now if nil (see now)
	clock func() time.Time
	// index is the search index of the catalog searched last (see searchIndex)
	index atomic.Pointer[searchIndex]
}

// register adds the tools to the server, one line each.
func (t *tools) register(r *registry) {
	add(r, getDatasetInfoTool(), t.getDatasetInfo)
	add(r, searchItemsTool(), t.searchItems)
	add(r, searchStagesTool(), t.searchStages)
}

// registry adds the tools to a server and keeps their names, the values of the tool label of the metrics.
type registry struct {
	server *sdkmcp.Server
	names  map[string]struct{}
}

// add adds a tool with the annotations every tool has: it only reads, the same arguments give the same result until
// the data changes, and it reaches nothing but the data of Penguin Statistics (a closed world).
func add[In, Out any](r *registry, tool *sdkmcp.Tool, h sdkmcp.ToolHandlerFor[In, Out]) {
	tool.Annotations = &sdkmcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(bool)}
	sdkmcp.AddTool(r.server, tool, h)
	r.names[tool.Name] = struct{}{}
}

// toolLabel bounds the tool label of the metrics to the tools of the server, as a client may call any name.
func (r *registry) toolLabel(name string) string {
	if _, ok := r.names[name]; ok {
		return name
	}
	return "other"
}

// NewServer returns the MCP server of data. site is the origin of the stats pages the results link to, version the
// version of the backend.
func NewServer(data Data, site, version string) *sdkmcp.Server {
	s, _ := newServer(&tools{data: data, site: strings.TrimRight(site, "/")}, version)
	return s
}

// newServer returns the MCP server of t, and its registry for tests to add tools to before it serves.
func newServer(t *tools, version string) (*sdkmcp.Server, *registry) {
	s := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:       serverName,
		Title:      serverTitle,
		Version:    version,
		WebsiteURL: t.site,
	}, &sdkmcp.ServerOptions{
		Instructions: instructions,
		// tools only, and their list never changes while the server runs: a subscriptions/listen of the 2026-07-28
		// protocol would otherwise wait for a change until the request times out (see observe). No logging either.
		Capabilities: &sdkmcp.ServerCapabilities{Tools: &sdkmcp.ToolCapabilities{}},
		Logger:       sdkLogger,
		// the lists (and the discovery of the 2026-07-28 protocol) are the same for everyone until a release
		SetCacheable: func(_ context.Context, _ sdkmcp.Request, c *sdkmcp.Cacheable) {
			c.TTLMs = int(listTTL.Milliseconds())
			c.CacheScope = "public"
		},
	})
	r := &registry{server: s, names: make(map[string]struct{})}
	t.register(r)
	// added last, so outermost: it also covers the middlewares of the SDK
	s.AddReceivingMiddleware(observe(r))
	return s, r
}
