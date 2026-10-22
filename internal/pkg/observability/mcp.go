package observability

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The outcomes of the JSON-RPC messages the MCP server (/mcp) handles
const (
	MCPOutcomeOK = "ok"
	// MCPOutcomeToolError is a tool call answered with a result that is an error (isError): a bad argument, an
	// unknown ID
	MCPOutcomeToolError = "tool_error"
	// MCPOutcomeError is a message answered with a JSON-RPC error: an unknown method or tool, a result that could
	// not be sent
	MCPOutcomeError = "error"
	// MCPOutcomeCold is a tool call that needed data not loaded or calculated yet, after a start
	MCPOutcomeCold = "cold"
	// MCPOutcomePanic is a message whose handling panicked, answered with a JSON-RPC internal error
	MCPOutcomePanic = "panic"
	// MCPOutcomeTimeout is a message answered after its request timed out, whose answer was not sent
	MCPOutcomeTimeout = "timeout"
)

var (
	MCPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "mcp", "requests_total"),
		Help: "JSON-RPC requests and notifications handled by the MCP server by method (see MCPMethodLabel), client " +
			"(see MCPClientLabel) and outcome (ok, tool_error, error, cold, panic, timeout)",
	}, []string{"method", "client", "outcome"})
	// MCPToolDuration covers the tool calls, from after the arguments were read to the result; tool is a tool of
	// the server or "other"
	MCPToolDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "mcp", "tool_duration_seconds"),
		Help:    "Duration of MCP tool calls in seconds by tool and outcome (ok, tool_error, error, cold, panic, timeout)",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 14),
	}, []string{"tool", "outcome"})
)

// mcpMethods are the methods of the MCP protocol a client sends to a server
var mcpMethods = map[string]struct{}{
	"initialize":                       {},
	"notifications/initialized":        {},
	"notifications/cancelled":          {},
	"ping":                             {},
	"server/discover":                  {},
	"subscriptions/listen":             {},
	"tools/list":                       {},
	"tools/call":                       {},
	"resources/list":                   {},
	"resources/templates/list":         {},
	"resources/read":                   {},
	"prompts/list":                     {},
	"prompts/get":                      {},
	"completion/complete":              {},
	"logging/setLevel":                 {},
	"notifications/roots/list_changed": {},
}

// MCPMethodLabel bounds the method label of the MCP metrics to the methods of the protocol, as a client may send
// any method.
func MCPMethodLabel(method string) string {
	if _, ok := mcpMethods[method]; ok {
		return method
	}
	return "other"
}

// mcpClients are the labels of the clients known by a part of the name they give in their clientInfo, lowercased,
// in the order they are matched in
var mcpClients = []struct{ part, label string }{
	{"claude-code", "claude-code"},
	{"claude", "claude-ai"},
	{"openai", "chatgpt"},
	{"chatgpt", "chatgpt"},
	{"cursor", "cursor"},
	{"visual studio code", "vscode"},
	{"vscode", "vscode"},
	{"gemini", "gemini"},
	{"inspector", "inspector"},
}

// MCPClientLabel bounds the client label of the MCP metrics to a few known clients, by the name a client gives in
// its clientInfo: "unknown" for no name, which the 2025 protocols send only with initialize, and "other" for the
// rest. The names themselves are only logged.
func MCPClientLabel(name string) string {
	if name == "" {
		return "unknown"
	}
	name = strings.ToLower(name)
	for _, c := range mcpClients {
		if strings.Contains(name, c.part) {
			return c.label
		}
	}
	return "other"
}
