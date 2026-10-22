package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

const site = "https://penguin-stats.io"

var lastModified = time.Date(2026, 10, 22, 16, 32, 1, 0, time.UTC)

type fakeData struct {
	catalog  *service.SEOCatalog
	matrices map[string]*service.SEOMatrix
}

func (d *fakeData) Catalog() *service.SEOCatalog            { return d.catalog }
func (d *fakeData) Matrix(server string) *service.SEOMatrix { return d.matrices[server] }

func testMatrices() map[string]*service.SEOMatrix {
	return map[string]*service.SEOMatrix{
		"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
			{StageID: "main_01-07", ItemID: "30012", Times: 1061347, Quantity: 1322056, StartTime: 1556676000000},
		}, lastModified),
	}
}

// testServer returns a server of data, and its registry for tests to add tools to.
func testServer(data Data) (*registry, *fiber.App) {
	srv, r := newServer(&tools{data: data, site: site}, "test")
	app := fiber.New()
	Mount(app, srv, 1000, time.Minute)
	return r, app
}

// probeIn is the input of the tool probe of the tests: n, 1 by default
type probeIn struct {
	N int `json:"n,omitempty"`
}

// addProbe adds the tool probe, which answers its input, to r, and returns the number of its calls.
func addProbe(r *registry) *atomic.Int64 {
	in, err := jsonschema.For[probeIn](nil)
	if err != nil {
		panic(err)
	}
	in.Properties["n"].Default = json.RawMessage("1")
	var calls atomic.Int64
	add(r, &sdkmcp.Tool{Name: "probe", Title: "Probe", Description: "Answers its input.", InputSchema: in},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in probeIn) (*sdkmcp.CallToolResult, probeIn, error) {
			calls.Add(1)
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: strconv.Itoa(in.N)}}}, in, nil
		})
	return &calls
}

const (
	legacyVersion = "2025-11-25"
	newVersion    = "2026-07-28"
)

// post posts a JSON-RPC message to /mcp as an MCP client does, with the headers given
func post(t *testing.T, app *fiber.App, body string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAccept, "application/json, text/event-stream")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(t *testing.T, b []byte) rpcResponse {
	t.Helper()
	var r rpcResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not a JSON-RPC response: %v: %s", err, b)
	}
	return r
}

// toolResult is a CallToolResult as sent
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// callTool calls a tool with the 2025-11-25 protocol, which needs no handshake when stateless, and returns its result
func callTool(t *testing.T, app *fiber.App, name, args string) toolResult {
	t.Helper()
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`,
		"Mcp-Protocol-Version", legacyVersion)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	r := decode(t, b)
	if r.Error != nil {
		t.Fatalf("JSON-RPC error %d %s", r.Error.Code, r.Error.Message)
	}
	var res toolResult
	if err := json.Unmarshal(r.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// text is the text content of a result, which every tool gives as one block
func (r toolResult) text(t *testing.T) string {
	t.Helper()
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("content: %+v", r.Content)
	}
	return r.Content[0].Text
}

func histogramCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}
