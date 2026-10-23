package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/service"
)

func TestRoundedNeverBreaksTheJSON(t *testing.T) {
	for _, tc := range []struct {
		x      float64
		ok     bool
		places int
		want   string
	}{
		{1.24567, true, 4, "1.2457"},
		{4.81813, true, 3, "4.818"},
		{0.00004, true, 4, "0"},
		{1, false, 4, "null"},
		{math.NaN(), true, 4, "null"},
		{math.Inf(1), true, 4, "null"},
		{6.0 / 0.0000001, true, 3, "60000000"},
	} {
		b, err := json.Marshal(rounded(tc.x, tc.ok, tc.places))
		if err != nil {
			t.Fatalf("rounded(%v): %v", tc.x, err)
		}
		if string(b) != tc.want {
			t.Errorf("rounded(%v, %v, %d) = %s, want %s", tc.x, tc.ok, tc.places, b, tc.want)
		}
	}
}

func TestTimes(t *testing.T) {
	if got := utcTime(1556676000000); got != "2019-05-01T02:00:00Z" {
		t.Errorf("utcTime = %q", got)
	}
	if got := dataAsOf(service.NewSEOMatrix("CN", nil, lastModified.In(time.FixedZone("UTC+8", 8*3600)))); got != "2026-10-22T16:32:01Z" {
		t.Errorf("dataAsOf = %q", got)
	}
	if got := dataAsOf(service.NewSEOMatrix("CN", nil, time.Time{})); got != "" {
		t.Errorf("dataAsOf without a time = %q", got)
	}
	if got := dataAsOf(nil); got != "" {
		t.Errorf("dataAsOf(nil) = %q", got)
	}
}

func TestTable(t *testing.T) {
	got := table([]string{"stage", "rate"}, [][]string{{"1-7", number(1.2457)}, {"a|b\nc", ""}})
	want := "| stage | rate |\n|---|---|\n| 1-7 | 1.2457 |\n| a\\|b c | - |"
	if got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

func TestSource(t *testing.T) {
	tl := &tools{site: site}
	s := tl.source("")
	if s.URL != site || s.License != "CC BY-NC 4.0" || s.LicenseURL != "https://creativecommons.org/licenses/by-nc/4.0/" ||
		s.Attribution != "Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0" {
		t.Errorf("source: %+v", s)
	}
	page := site + "/en/result/cn/item/30012"
	if got := tl.source(page); got.URL != page || got.Attribution != s.Attribution {
		t.Errorf("source of a page: %+v", got)
	}
	if got, want := tl.source(page).line(), "Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 "+
		"(https://creativecommons.org/licenses/by-nc/4.0/). Source: "+page; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestColdData(t *testing.T) {
	tl := &tools{data: &fakeData{}}
	if _, err := tl.catalog(); !errors.Is(err, errCold) {
		t.Errorf("catalog: %v", err)
	}
	if _, err := tl.matrix("CN"); !errors.Is(err, errCold) {
		t.Errorf("matrix: %v", err)
	}
	tl.data = &fakeData{catalog: testCatalog(), matrices: testMatrices()}
	if c, err := tl.catalog(); c == nil || err != nil {
		t.Errorf("catalog: %v", err)
	}
	if m, err := tl.matrix("CN"); m == nil || err != nil {
		t.Errorf("matrix: %v", err)
	}
	if _, err := tl.matrix("US"); !errors.Is(err, errCold) {
		t.Errorf("matrix of US: %v", err)
	}
}

type argsIn struct {
	Query  string `json:"query" jsonschema:"What to look for"`
	Server string `json:"server,omitempty"`
	Lang   string `json:"lang,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func TestInputSchema(t *testing.T) {
	s := inputSchema[argsIn](map[string]property{
		"query":  lengthProperty(1, 64),
		"server": rankingServerProperty,
		"lang":   langProperty,
		"limit":  intProperty(10, 1, 25),
	})
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Required             []string `json:"required"`
		AdditionalProperties any      `json:"additionalProperties"`
		Properties           map[string]struct {
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
			Default     any      `json:"default"`
			Minimum     *float64 `json:"minimum"`
			Maximum     *float64 `json:"maximum"`
			MinLength   *int     `json:"minLength"`
			MaxLength   *int     `json:"maxLength"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Required) != 1 || got.Required[0] != "query" || got.AdditionalProperties != false {
		t.Errorf("schema: %s", b)
	}
	q, srv, lang, limit := got.Properties["query"], got.Properties["server"], got.Properties["lang"], got.Properties["limit"]
	if q.Description != "What to look for" || *q.MinLength != 1 || *q.MaxLength != 64 {
		t.Errorf("query: %+v", q)
	}
	if len(srv.Enum) != 4 || srv.Enum[0] != "CN" || srv.Default != "CN" || srv.Description == "" {
		t.Errorf("server: %+v", srv)
	}
	// no default for the language: models read one as leave it out (see langProperty)
	if len(lang.Enum) != 4 || lang.Default != nil || lang.Description == "" {
		t.Errorf("lang: %+v", lang)
	}
	if limit.Default != 10.0 || *limit.Minimum != 1 || *limit.Maximum != 25 {
		t.Errorf("limit: %+v", limit)
	}

	defer func() {
		if recover() == nil {
			t.Error("no panic for a property the input does not have")
		}
	}()
	inputSchema[argsIn](map[string]property{"top_n": intProperty(10, 1, 100)})
}

// The SDK fills in the defaults of the schema and rejects the arguments outside it, as results that are errors.
func TestInputSchemaThroughTheSDK(t *testing.T) {
	r, app := testServer(&fakeData{})
	add(r, &sdkmcp.Tool{Name: "echo", InputSchema: inputSchema[argsIn](map[string]property{
		"server": rankingServerProperty,
		"lang":   langProperty,
		"limit":  intProperty(10, 1, 25),
	})}, func(_ context.Context, _ *sdkmcp.CallToolRequest, in argsIn) (*sdkmcp.CallToolResult, argsIn, error) {
		return textResult(in.Query), in, nil
	})

	res := callTool(t, app, "echo", `{"query":"orirock"}`)
	var got argsIn
	if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
		t.Fatal(err)
	}
	if res.IsError || got != (argsIn{Query: "orirock", Server: "CN", Limit: 10}) {
		t.Errorf("defaults: %+v %s", got, res.StructuredContent)
	}
	if serverOf("") != "CN" || serverOf("JP") != "JP" || langOf("") != "en" || langOf("ko") != "ko" {
		t.Error("the defaults of handlers called without the SDK")
	}

	for _, args := range []string{
		`{"query":"orirock","server":"cn"}`,
		`{"query":"orirock","lang":"fr"}`,
		`{"query":"orirock","limit":26}`,
		`{"query":"orirock","item":"30012"}`,
		`{}`,
	} {
		if res := callTool(t, app, "echo", args); !res.IsError {
			t.Errorf("%s: not an error: %s", args, res.StructuredContent)
		}
	}
}

type serverIn struct {
	Server string `json:"server"`
}

// The server of the tools whose answer depends on it is required and has no default: a call without it is an error
// naming it, for the model to ask the user rather than to answer for CN.
func TestRequiredServer(t *testing.T) {
	s := inputSchema[serverIn](map[string]property{"server": serverProperty})
	if !slices.Equal(s.Required, []string{"server"}) || s.Properties["server"].Default != nil || len(s.Properties["server"].Enum) != 4 ||
		!strings.Contains(s.Properties["server"].Description, "ask the user") {
		t.Errorf("schema: %+v %+v", s, s.Properties["server"])
	}

	r, app := testServer(&fakeData{})
	add(r, &sdkmcp.Tool{Name: "echo", InputSchema: s},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in serverIn) (*sdkmcp.CallToolResult, serverIn, error) {
			return textResult(in.Server), in, nil
		})
	for _, args := range []string{`{}`, "null"} {
		if res := callTool(t, app, "echo", args); !res.IsError || !strings.Contains(res.text(t), `"server"`) {
			t.Errorf("%s: %+v", args, res)
		}
	}
	if res := callTool(t, app, "echo", `{"server":"JP"}`); res.IsError || res.text(t) != "JP" {
		t.Errorf("JP: %+v", res)
	}
}
