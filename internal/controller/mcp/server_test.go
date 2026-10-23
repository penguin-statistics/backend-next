package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/service"
)

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},` +
	`"clientInfo":{"name":"claude-ai","version":"0.1.0"}}}`

// wantServerInfo is what the server tells of itself: in the result of initialize, and in the _meta of every result of
// the 2026-07-28 protocol
var wantServerInfo = &sdkmcp.Implementation{
	Name:        "penguin-stats",
	Title:       "Penguin Statistics",
	Description: "Crowdsourced Arknights drop rates by stage and item, for the CN, US, JP and KR game servers",
	Version:     "test",
	WebsiteURL:  site,
	Icons: []sdkmcp.Icon{
		{Source: "https://penguin-stats.io/favicon/android-chrome-512x512.png", MIMEType: "image/png", Sizes: []string{"512x512"}},
		{Source: "https://penguin-stats.io/favicon/android-chrome-192x192.png", MIMEType: "image/png", Sizes: []string{"192x192"}},
	},
}

// meta is the _meta every message of the 2026-07-28 protocol carries
const meta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},` +
	`"io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.0"}}`

func TestInitialize(t *testing.T) {
	_, app := testServer(&fakeData{})
	resp, b := post(t, app, initialize)
	if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderContentType) != fiber.MIMEApplicationJSON {
		t.Fatalf("status %d, Content-Type %q: %s", resp.StatusCode, resp.Header.Get(fiber.HeaderContentType), b)
	}
	// stateless: no session
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		t.Errorf("Mcp-Session-Id %q", id)
	}
	var res struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		Capabilities    json.RawMessage        `json:"capabilities"`
		ServerInfo      *sdkmcp.Implementation `json:"serverInfo"`
		Instructions    string                 `json:"instructions"`
	}
	if err := json.Unmarshal(decode(t, b).Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != legacyVersion {
		t.Errorf("protocol version %q", res.ProtocolVersion)
	}
	// tools only, without listChanged (see subscriptions/listen) or logging
	if string(res.Capabilities) != `{"tools":{}}` {
		t.Errorf("capabilities %s", res.Capabilities)
	}
	if !reflect.DeepEqual(res.ServerInfo, wantServerInfo) {
		t.Errorf("server info %+v", res.ServerInfo)
	}
	if n := len(res.Instructions); n > 600 || !strings.Contains(res.Instructions, "CC BY-NC 4.0") {
		t.Errorf("instructions (%d bytes): %s", n, res.Instructions)
	}
	// they tell the workflow: every tool
	for _, tool := range []string{"get_dataset_info", "search_items", "search_stages", "get_item_drop_rates", "get_stage_drops", "list_events"} {
		if !strings.Contains(res.Instructions, tool) {
			t.Errorf("instructions lack %s: %s", tool, res.Instructions)
		}
	}
}

func TestToolsList(t *testing.T) {
	_, app := testServer(&fakeData{})
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "Mcp-Protocol-Version", legacyVersion)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var res struct {
		Tools []struct {
			Name, Title, Description string
			InputSchema              map[string]any `json:"inputSchema"`
			OutputSchema             map[string]any `json:"outputSchema"`
			Annotations              map[string]any `json:"annotations"`
		} `json:"tools"`
		TTLMs      int    `json:"ttlMs"`
		CacheScope string `json:"cacheScope"`
	}
	if err := json.Unmarshal(decode(t, b).Result, &res); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	// meta#20: the order of tools/list is fixed (the SDK sorts it by name)
	if !slices.Equal(names, []string{"get_dataset_info", "get_item_drop_rates", "get_stage_drops", "list_events", "search_items", "search_stages"}) {
		t.Errorf("tools %q", names)
	}
	for _, tool := range res.Tools {
		if tool.Title == "" || tool.Description == "" {
			t.Errorf("%s: title %q, description %q", tool.Name, tool.Title, tool.Description)
		}
		// every hint given rather than left to the defaults, and the title there too
		if want := map[string]any{"title": tool.Title, "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true,
			"openWorldHint": false}; !maps.Equal(tool.Annotations, want) {
			t.Errorf("%s: annotations %v, want %v", tool.Name, tool.Annotations, want)
		}
		// properties even without arguments: some clients require them of an object
		if _, ok := tool.InputSchema["properties"]; !ok || tool.InputSchema["type"] != "object" || tool.OutputSchema["type"] != "object" {
			t.Errorf("%s: input schema %v, output schema %v", tool.Name, tool.InputSchema, tool.OutputSchema)
		}
	}
	if res.TTLMs != 3600000 || res.CacheScope != "public" {
		t.Errorf("ttlMs %d, cacheScope %q", res.TTLMs, res.CacheScope)
	}
}

func TestNoSessionsNorStreams(t *testing.T) {
	_, app := testServer(&fakeData{})
	for _, method := range []string{fiber.MethodGet, fiber.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp", nil)
		req.Header.Set(fiber.HeaderAccept, "text/event-stream")
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusMethodNotAllowed || resp.Header.Get(fiber.HeaderAllow) != fiber.MethodPost {
			t.Errorf("%s: status %d, Allow %q", method, resp.StatusCode, resp.Header.Get(fiber.HeaderAllow))
		}
	}
}

func TestBodyLimit(t *testing.T) {
	_, app := testServer(&fakeData{})
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":{},"_pad":"` +
		strings.Repeat("x", maxBodyBytes) + `"}}`
	if resp, b := post(t, app, big, "Mcp-Protocol-Version", legacyVersion); resp.StatusCode != fiber.StatusRequestEntityTooLarge {
		t.Errorf("status %d: %.200s", resp.StatusCode, b)
	}
}

// A batch of messages would run all its calls for one request of the rate and concurrency limits: it is refused
// before the SDK reads it, whatever the protocol version of the request.
func TestBatchesAreRejected(t *testing.T) {
	r, app := testServer(&fakeData{})
	calls := addProbe(r)
	batch := " \n[" +
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":{}}},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"probe","arguments":{}}},` +
		`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "]"
	for _, version := range []string{"", "2024-11-05", "2025-03-26", legacyVersion} {
		var header []string
		if version != "" {
			header = []string{"Mcp-Protocol-Version", version}
		}
		resp, b := post(t, app, batch, header...)
		if r := decode(t, b); resp.StatusCode != fiber.StatusBadRequest || r.Error == nil || r.Error.Code != -32600 || r.ID != nil {
			t.Errorf("version %q: status %d: %s", version, resp.StatusCode, b)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the probe ran %d times", n)
	}

	// one message is served
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"probe","arguments":{}}}`)
	if r := decode(t, b); resp.StatusCode != fiber.StatusOK || r.Error != nil {
		t.Errorf("one message: status %d: %s", resp.StatusCode, b)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the probe ran %d times", n)
	}
}

// A subscriptions/listen of the 2026-07-28 protocol would wait for a change of the tools until the request timed
// out; it is rejected at once instead.
func TestSubscriptionsListenIsRejected(t *testing.T) {
	_, app := testServer(&fakeData{})
	start := time.Now()
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":3,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true},`+meta+`}}`,
		"Mcp-Protocol-Version", newVersion, "Mcp-Method", "subscriptions/listen")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("answered after %s", d)
	}
	if resp.StatusCode != fiber.StatusNotFound || !bytes.Contains(b, []byte(`-32601`)) {
		t.Errorf("status %d: %s", resp.StatusCode, b)
	}
}

// A request whose answer misses the deadline is answered 503, to retry, rather than an empty 200; the message counts
// as timed out.
func TestRequestTimeout(t *testing.T) {
	prev := requestTimeout
	requestTimeout = 200 * time.Millisecond
	t.Cleanup(func() { requestTimeout = prev })

	r, app := testServer(&fakeData{})
	add(r, &sdkmcp.Tool{Name: "slow", Title: "Slow"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, struct{}, error) {
		time.Sleep(400 * time.Millisecond)
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "late"}}}, struct{}{}, nil
	})

	timeouts := observability.MCPRequests.WithLabelValues("tools/call", "unknown", observability.MCPOutcomeTimeout)
	before := testutil.ToFloat64(timeouts)
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"slow","arguments":{}}}`, "Mcp-Protocol-Version", legacyVersion)
	res := decode(t, b)
	if resp.StatusCode != fiber.StatusServiceUnavailable || resp.Header.Get(fiber.HeaderRetryAfter) != "1" || res.Error == nil ||
		res.Error.Code != -32000 || res.Error.Message != "timed out, retry" || res.ID != float64(5) {
		t.Errorf("status %d, Retry-After %q: %s", resp.StatusCode, resp.Header.Get(fiber.HeaderRetryAfter), b)
	}
	if got := testutil.ToFloat64(timeouts) - before; got != 1 {
		t.Errorf("timeouts counted %v times", got)
	}
}

// The error a request gets past the SDK carries its ID when the body has one.
func TestRequestID(t *testing.T) {
	for body, want := range map[string]string{
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`:     "5",
		`{"jsonrpc":"2.0","id":"a-1","method":"ping"}`: `"a-1"`,
		`{"jsonrpc":"2.0","method":"notifications/x"}`: "",
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`:  "",
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:   "",
		`{"jsonrpc":"2.0","id":5,"method":"ping"`:      "",
	} {
		if got := string(requestID([]byte(body))); got != want {
			t.Errorf("%s: ID %q, want %q", body, got, want)
		}
	}
}

// behindIngress is the config of an app whose requests come through the ingress, which sets the client's IP
var behindIngress = fiber.Config{ProxyHeader: "X-Original-Forwarded-For", EnableTrustedProxyCheck: true, TrustedProxies: []string{"0.0.0.0/0"}}

func TestRateLimit(t *testing.T) {
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	app := fiber.New(behindIngress)
	Mount(app, srv, 2, time.Minute)

	const client = "X-Original-Forwarded-For"
	for i := 0; i < 2; i++ {
		if resp, b := post(t, app, initialize, client, "203.0.113.7"); resp.StatusCode != fiber.StatusOK {
			t.Fatalf("request %d: status %d: %s", i, resp.StatusCode, b)
		}
	}
	resp, b := post(t, app, initialize, client, "203.0.113.7")
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	retryAfter, err := strconv.Atoi(resp.Header.Get(fiber.HeaderRetryAfter))
	if err != nil || retryAfter < 1 || retryAfter > 60 {
		t.Errorf("Retry-After %q", resp.Header.Get(fiber.HeaderRetryAfter))
	}
	r := decode(t, b)
	if r.JSONRPC != "2.0" || r.ID != nil || r.Error == nil || r.Error.Code != -32000 ||
		r.Error.Message != "rate limited, retry after "+strconv.Itoa(retryAfter)+"s" {
		t.Errorf("body %s", b)
	}

	// another client is not limited
	if resp, b := post(t, app, initialize, client, "203.0.113.8"); resp.StatusCode != fiber.StatusOK {
		t.Errorf("another client: status %d: %s", resp.StatusCode, b)
	}
}

// Retry-After is enough: a client that waits it out is served, as the window it was refused in has reset.
func TestRateLimitRetryAfterIsEnough(t *testing.T) {
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	app := fiber.New(behindIngress)
	Mount(app, srv, 2, 3*time.Second)

	const client = "X-Original-Forwarded-For"
	var ra int
	for i, want := range []int{fiber.StatusOK, fiber.StatusOK, fiber.StatusTooManyRequests, fiber.StatusTooManyRequests,
		fiber.StatusTooManyRequests, fiber.StatusTooManyRequests} {
		resp, b := post(t, app, initialize, client, "203.0.113.9")
		if resp.StatusCode != want {
			t.Fatalf("request %d: status %d, want %d: %s", i, resp.StatusCode, want, b)
		}
		if want == fiber.StatusTooManyRequests {
			var err error
			if ra, err = strconv.Atoi(resp.Header.Get(fiber.HeaderRetryAfter)); err != nil || ra < 1 || ra > 3 {
				t.Fatalf("request %d: Retry-After %q", i, resp.Header.Get(fiber.HeaderRetryAfter))
			}
		}
	}
	// a sliding window would still refuse it: the refused requests count in the next window
	time.Sleep(time.Duration(ra)*time.Second + 1200*time.Millisecond)
	if resp, b := post(t, app, initialize, client, "203.0.113.9"); resp.StatusCode != fiber.StatusOK {
		t.Errorf("after Retry-After %ds: status %d: %s", ra, resp.StatusCode, b)
	}
}

func TestClientKey(t *testing.T) {
	app := fiber.New(behindIngress)
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString(clientKey(c)) })
	for _, tc := range []struct{ header, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"203.0.113.7, 10.0.0.1", "203.0.113.7"},
		// a host has its /64
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"},
		{"::ffff:203.0.113.7", "203.0.113.7"},
		// without the proxy header, the peer
		{"", "0.0.0.0"},
		{"garbage", "0.0.0.0"},
	} {
		req := httptest.NewRequest(fiber.MethodGet, "/", nil)
		if tc.header != "" {
			req.Header.Set("X-Original-Forwarded-For", tc.header)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		resp.Body.Close()
		if b.String() != tc.want {
			t.Errorf("%q: key %q, want %q", tc.header, b.String(), tc.want)
		}
	}
}

func TestBusy(t *testing.T) {
	app := fiber.New()
	app.Post("/mcp", busy)
	resp, b := post(t, app, initialize)
	if r := decode(t, b); resp.StatusCode != fiber.StatusTooManyRequests || resp.Header.Get(fiber.HeaderRetryAfter) != "1" ||
		r.Error == nil || r.Error.Code != -32000 {
		t.Errorf("status %d, Retry-After %q: %s", resp.StatusCode, resp.Header.Get(fiber.HeaderRetryAfter), b)
	}
}

// A tool that needs data not loaded yet answers so, as an error the model can act on.
func TestColdToolCall(t *testing.T) {
	data := &fakeData{}
	r, app := testServer(data)
	tl := &tools{data: data, site: site}
	add(r, &sdkmcp.Tool{Name: "needs_matrix", Title: "Needs a matrix"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, struct{}, error) {
		if _, err := tl.matrix("CN"); err != nil {
			return nil, struct{}{}, err
		}
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, struct{}{}, nil
	})

	cold := observability.MCPRequests.WithLabelValues("tools/call", "unknown", observability.MCPOutcomeCold)
	before := testutil.ToFloat64(cold)
	durations := histogramCount(t, observability.MCPToolDuration.WithLabelValues("needs_matrix", observability.MCPOutcomeCold))
	res := callTool(t, app, "needs_matrix", `{}`)
	if !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("result %+v", res)
	}
	if got := testutil.ToFloat64(cold) - before; got != 1 {
		t.Errorf("cold requests counted %v times", got)
	}
	if got := histogramCount(t, observability.MCPToolDuration.WithLabelValues("needs_matrix", observability.MCPOutcomeCold)) - durations; got != 1 {
		t.Errorf("cold durations observed %d times", got)
	}

	data.matrices = testMatrices()
	if res := callTool(t, app, "needs_matrix", `{}`); res.IsError {
		t.Errorf("result once warm %+v", res)
	}
}

// A panic in a tool, on a goroutine of the SDK, would end the process: it is answered with an internal error.
func TestPanicInATool(t *testing.T) {
	r, app := testServer(&fakeData{})
	add(r, &sdkmcp.Tool{Name: "boom", Title: "Boom"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, struct{}, error) {
		var m map[string]int
		m["boom"]++
		return nil, struct{}{}, nil
	})

	panics := observability.MCPRequests.WithLabelValues("tools/call", "unknown", observability.MCPOutcomePanic)
	before := testutil.ToFloat64(panics)
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"boom","arguments":{}}}`, "Mcp-Protocol-Version", legacyVersion)
	res := decode(t, b)
	if resp.StatusCode != fiber.StatusOK || res.Error == nil || res.Error.Code != -32603 || res.Error.Message != "internal error" {
		t.Errorf("status %d: %s", resp.StatusCode, b)
	}
	if got := testutil.ToFloat64(panics) - before; got != 1 {
		t.Errorf("panics counted %v times", got)
	}
	// still serving
	addProbe(r)
	if res := callTool(t, app, "probe", `{}`); res.IsError {
		t.Errorf("after the panic: %+v", res)
	}
}

// "arguments": null, or none, is no arguments: the defaults of the input schema apply, where the SDK would panic.
func TestNullArguments(t *testing.T) {
	r, app := testServer(&fakeData{})
	addProbe(r)
	panics := observability.MCPRequests.WithLabelValues("tools/call", "unknown", observability.MCPOutcomePanic)
	before := testutil.ToFloat64(panics)
	for _, params := range []string{
		`{"name":"probe","arguments":null}`,
		`{"name":"probe","arguments": null }`,
		`{"name":"probe"}`,
	} {
		resp, b := post(t, app, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":`+params+`}`, "Mcp-Protocol-Version", legacyVersion)
		r := decode(t, b)
		if resp.StatusCode != fiber.StatusOK || r.Error != nil {
			t.Errorf("%s: status %d: %s", params, resp.StatusCode, b)
			continue
		}
		var res toolResult
		if err := json.Unmarshal(r.Result, &res); err != nil || res.IsError || string(res.StructuredContent) != `{"n":1}` {
			t.Errorf("%s: result %s %v", params, r.Result, err)
		}
	}
	if got := testutil.ToFloat64(panics) - before; got != 0 {
		t.Errorf("panics counted %v times", got)
	}
}

// echoIn is the input of the tool echo of TestNumbersForStrings
type echoIn struct {
	ID string `json:"id"`
	N  int    `json:"n,omitempty"`
}

// A number given for a string argument (an item ID such as 30012) is read as that string; other types are still
// rejected against the input schema.
func TestNumbersForStrings(t *testing.T) {
	r, app := testServer(&fakeData{})
	in, err := jsonschema.For[echoIn](nil)
	if err != nil {
		t.Fatal(err)
	}
	add(r, &sdkmcp.Tool{Name: "echo", Title: "Echo", InputSchema: in},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in echoIn) (*sdkmcp.CallToolResult, echoIn, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: in.ID}}}, in, nil
		})
	for args, want := range map[string]string{
		`{"id":30012}`:         `{"id":"30012"}`,
		`{"id":-1.5e3,"n":2}`:  `{"id":"-1.5e3","n":2}`,
		`{"id":"30012","n":2}`: `{"id":"30012","n":2}`,
		`{"id":true}`:          "",
		`{"id":"a","n":"2"}`:   "",
	} {
		_, b := post(t, app, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"echo","arguments":`+args+`}}`, "Mcp-Protocol-Version", legacyVersion)
		var res toolResult
		if r := decode(t, b); r.Error != nil || json.Unmarshal(r.Result, &res) != nil {
			t.Errorf("%s: %s", args, b)
			continue
		}
		if want == "" {
			if !res.IsError {
				t.Errorf("%s: accepted: %s", args, res.StructuredContent)
			}
		} else if res.IsError || string(res.StructuredContent) != want {
			t.Errorf("%s: result %s, want %s", args, res.StructuredContent, want)
		}
	}
}

func TestUnknownToolIsAProtocolError(t *testing.T) {
	_, app := testServer(&fakeData{})
	errs := observability.MCPRequests.WithLabelValues("tools/call", "unknown", observability.MCPOutcomeError)
	durations := histogramCount(t, observability.MCPToolDuration.WithLabelValues("other", observability.MCPOutcomeError))
	before := testutil.ToFloat64(errs)
	_, b := post(t, app, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"drop_tables","arguments":{}}}`, "Mcp-Protocol-Version", legacyVersion)
	if r := decode(t, b); r.Error == nil || r.Error.Code != -32602 {
		t.Errorf("response %s", b)
	}
	if got := testutil.ToFloat64(errs) - before; got != 1 {
		t.Errorf("errors counted %v times", got)
	}
	// the name a client called is not a label
	if got := histogramCount(t, observability.MCPToolDuration.WithLabelValues("other", observability.MCPOutcomeError)) - durations; got != 1 {
		t.Errorf("durations of other tools observed %d times", got)
	}
}

// The request log tells which message a request carried, from which client, and how it was answered.
func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	srv, r := newServer(&tools{data: &fakeData{}, site: site}, "test")
	addProbe(r)
	app := fiber.New()
	middlewares.Logger(app)
	Mount(app, srv, 1000, time.Minute)

	for _, tc := range []struct {
		name   string
		send   func()
		fields map[string]any
	}{
		{"initialize", func() { post(t, app, initialize) }, map[string]any{
			"mcp.method": "initialize", "mcp.outcome": "ok", "mcp.protocol_version": legacyVersion,
			"mcp.client.name": "claude-ai", "mcp.client.version": "0.1.0", "level": "info",
		}},
		{"tools/call of the 2026-07-28 protocol", func() {
			post(t, app, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{`+meta+`,"name":"probe","arguments":{}}}`,
				"Mcp-Protocol-Version", newVersion, "Mcp-Method", "tools/call", "Mcp-Name", "probe")
		}, map[string]any{
			"mcp.method": "tools/call", "mcp.tool": "probe", "mcp.outcome": "ok", "mcp.protocol_version": newVersion,
			"mcp.client.name": "claude-code",
		}},
		{"tool error", func() {
			post(t, app, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"probe","arguments":{"server":"CN"}}}`,
				"Mcp-Protocol-Version", legacyVersion)
		}, map[string]any{
			"mcp.method": "tools/call", "mcp.tool": "probe", "mcp.outcome": "tool_error", "mcp.protocol_version": legacyVersion,
		}},
		// no event stream nor session: expected, not a warning
		{"GET", func() {
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/mcp", nil), 5000)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		}, map[string]any{"http.status_code": 405.0, "level": "info"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			tc.send()
			line := requestLogLine(t, &buf)
			for k, v := range tc.fields {
				if line[k] != v {
					t.Errorf("%s = %v, want %v", k, line[k], v)
				}
			}
			if tc.name == "tool error" && !strings.Contains(line["mcp.error"].(string), "server") {
				t.Errorf("mcp.error = %v", line["mcp.error"])
			}
		})
	}
}

// requestLogLine returns the single "http.request" log line in buf.
func requestLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if m["evt.name"] == "http.request" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want 1 request log, got %d: %s", len(found), buf.String())
	}
	return found[0]
}

func TestSDKLogsAreWarnings(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	sdkLogger.Info("server connecting")
	if buf.Len() != 0 {
		t.Errorf("info logged: %s", buf.String())
	}
	sdkLogger.With("method", "ping").Error("method removed in the new protocol")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	if line["level"] != "warn" || line["evt.name"] != "mcp.sdk" || line["sdk.level"] != "ERROR" || line["sdk.method"] != "ping" ||
		line["message"] != "method removed in the new protocol" {
		t.Errorf("log %v", line)
	}
}

func TestOutcomeOfAFailedCall(t *testing.T) {
	// a call that failed may leave a nil *CallToolResult in the result
	if got := outcomeOf((*sdkmcp.CallToolResult)(nil), errors.New("marshaling output")); got != observability.MCPOutcomeError {
		t.Errorf("outcome %q", got)
	}
	if got := outcomeOf((*sdkmcp.CallToolResult)(nil), nil); got != observability.MCPOutcomeOK {
		t.Errorf("outcome %q", got)
	}
	if got := clip("固源岩", 4); got != "固" {
		t.Errorf("clip %q", got)
	}
}

// fiberTransport sends the requests of a client to app, without a listener.
type fiberTransport struct{ app *fiber.App }

func (f fiberTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.app.Test(req, 5000)
}

// The SDK's client speaks both protocols with the server, through the whole middleware chain of the service.
func TestClients(t *testing.T) {
	conf := &appconfig.Config{}
	app := httpserver.CreateServiceApp(conf)
	srv, r := newServer(&tools{data: &fakeData{}, site: site}, "test")
	addProbe(r)
	Mount(app, srv, 1000, time.Minute)

	for _, tc := range []struct{ asked, want string }{
		// discovery (server/discover), then _meta on every message
		{"", newVersion},
		// the initialize handshake
		{legacyVersion, legacyVersion},
	} {
		t.Run("protocol "+tc.want, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "penguin-test", Version: "1"}, nil)
			cs, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
				Endpoint:   "http://mcp.test/mcp",
				HTTPClient: &http.Client{Transport: fiberTransport{app}},
				MaxRetries: -1,
			}, &sdkmcp.ClientSessionOptions{ProtocolVersion: tc.asked})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			if got := cs.InitializeResult().ProtocolVersion; got != tc.want {
				t.Errorf("protocol version %q, want %q", got, tc.want)
			}
			// the 2026-07-28 protocol has it in the _meta of the result of server/discover
			if got := cs.InitializeResult().ServerInfo; !reflect.DeepEqual(got, wantServerInfo) {
				t.Errorf("server info %+v", got)
			}

			tools, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(tools.Tools, func(tool *sdkmcp.Tool) bool { return tool.Name == "probe" }) {
				t.Errorf("tools %+v", tools.Tools)
			}

			// the client sends a nil map as "arguments": null (see observe)
			res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "probe", Arguments: map[string]any(nil)})
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := json.Marshal(res.StructuredContent); res.IsError || string(b) != `{"n":1}` {
				t.Errorf("result %s, isError %v", b, res.IsError)
			}
		})
	}
}

// Browsers may call the server from any page, with the headers of the MCP protocol.
func TestCORSPreflight(t *testing.T) {
	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = []string{"https://penguin-stats.io"}
	app := httpserver.CreateServiceApp(conf)
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	Mount(app, srv, 1, time.Minute)

	req := httptest.NewRequest(fiber.MethodOptions, "/mcp", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://penguin-stats.io")
	req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodPost)
	req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "content-type,mcp-protocol-version,mcp-method,mcp-name,mcp-param-item_id")
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != fiber.StatusNoContent || resp.Header.Get(fiber.HeaderAccessControlAllowOrigin) != "*" ||
		resp.Header.Get(fiber.HeaderAccessControlAllowCredentials) != "" ||
		resp.Header.Get(fiber.HeaderAccessControlAllowHeaders) != "content-type,mcp-protocol-version,mcp-method,mcp-name,mcp-param-item_id" {
		t.Errorf("status %d, headers %v", resp.StatusCode, resp.Header)
	}

	// the answers too, including a 429 that tells when to retry
	for i, want := range []int{fiber.StatusOK, fiber.StatusTooManyRequests} {
		resp, b := post(t, app, initialize, fiber.HeaderOrigin, "https://penguin-stats.io")
		if resp.StatusCode != want || resp.Header.Get(fiber.HeaderAccessControlAllowOrigin) != "*" ||
			!strings.Contains(resp.Header.Get(fiber.HeaderAccessControlExposeHeaders), fiber.HeaderRetryAfter) {
			t.Errorf("request %d: status %d, headers %v: %s", i, resp.StatusCode, resp.Header, b)
		}
		if want == fiber.StatusTooManyRequests && resp.Header.Get(fiber.HeaderRetryAfter) == "" {
			t.Error("no Retry-After")
		}
	}
}

func TestRegister(t *testing.T) {
	cache.Initialize()
	for _, tc := range []struct {
		enabled    bool
		publicURL  string
		status     int
		cardStatus int
	}{
		{enabled: false, status: fiber.StatusNotFound, cardStatus: fiber.StatusNotFound},
		{enabled: false, publicURL: publicURL, status: fiber.StatusNotFound, cardStatus: fiber.StatusNotFound},
		// no public URL, no card
		{enabled: true, status: fiber.StatusOK, cardStatus: fiber.StatusNotFound},
		{enabled: true, publicURL: publicURL, status: fiber.StatusOK, cardStatus: fiber.StatusOK},
	} {
		t.Run("enabled "+strconv.FormatBool(tc.enabled)+" public URL "+tc.publicURL, func(t *testing.T) {
			lc := fxtest.NewLifecycle(t)
			data := service.NewSEOData(lc, &appconfig.Config{}, nil, nil, nil, nil)
			app := fiber.New()
			err := Register(app, &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MCPEnabled: tc.enabled, SEOSiteURL: site,
				MCPPublicURL: tc.publicURL}}, data)
			if err != nil {
				t.Fatal(err)
			}
			resp, b := post(t, app, initialize)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, b)
			}
			if resp, b := request(t, app, fiber.MethodGet, "/mcp/server-card"); resp.StatusCode != tc.cardStatus {
				t.Errorf("card: status %d, want %d: %s", resp.StatusCode, tc.cardStatus, b)
			}
			// the guide with the card
			for _, path := range []string{"/", "/index.md", "/llms.txt", "/robots.txt"} {
				if resp, _ := request(t, app, fiber.MethodGet, "https://mcp.penguin-stats.io"+path); resp.StatusCode != tc.cardStatus {
					t.Errorf("guide %s: status %d, want %d", path, resp.StatusCode, tc.cardStatus)
				}
			}
			if !tc.enabled {
				return
			}
			// right after a start: nothing loaded, which the info tells
			res := callTool(t, app, "get_dataset_info", `{}`)
			var out datasetInfo
			if err := json.Unmarshal(res.StructuredContent, &out); err != nil || res.IsError || out.Servers[0].MatrixAvailable {
				t.Errorf("info %s %v", res.StructuredContent, err)
			}
		})
	}
}

// A public URL that is not one fails the startup, also while the server is off.
func TestRegisterRejectsAWrongPublicURL(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		err := Register(fiber.New(), &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MCPEnabled: enabled, SEOSiteURL: site,
			MCPPublicURL: "https://mcp.penguin-stats.io/"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "PENGUIN_V3_MCP_PUBLIC_URL") {
			t.Errorf("enabled %v: %v", enabled, err)
		}
	}
}
