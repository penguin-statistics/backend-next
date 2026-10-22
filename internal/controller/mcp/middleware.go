package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

const (
	methodCallTool            = "tools/call"
	methodSubscriptionsListen = "subscriptions/listen"
)

// observe is the outermost receiving middleware of the server. It answers a panic while handling a message with a
// JSON-RPC internal error: the SDK handles messages on goroutines of its own, out of reach of fiber's recover, where
// a panic would end the process. It rejects subscriptions/listen, as the tools never change. And it counts each
// message in the metrics and keeps it for the request log.
func observe(r *registry) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (res sdkmcp.Result, err error) {
			start := time.Now()
			defer func() {
				var outcome string
				if p := recover(); p != nil {
					log.Error().
						Str("evt.name", "mcp.panic").
						Interface("panic", p).
						Bytes("stack", debug.Stack()).
						Str("method", method).
						Msg("panicked handling an MCP message; answering it with an internal error")
					res, err = nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
					outcome = observability.MCPOutcomePanic
				} else {
					outcome = outcomeOf(res, err)
					if recordOf(ctx).expired() {
						// answered after the request timed out: the client got a 503 instead (see serve)
						outcome = observability.MCPOutcomeTimeout
					}
				}
				tool := ""
				if call, ok := req.(*sdkmcp.CallToolRequest); ok && call.Params != nil {
					tool = call.Params.Name
				}
				client, protocolVersion := clientOf(req, res)
				var clientName, clientVersion string
				if client != nil {
					clientName, clientVersion = client.Name, client.Version
				}
				observability.MCPRequests.WithLabelValues(observability.MCPMethodLabel(method), observability.MCPClientLabel(clientName), outcome).Inc()
				if method == methodCallTool {
					observability.MCPToolDuration.WithLabelValues(r.toolLabel(tool), outcome).Observe(time.Since(start).Seconds())
				}
				recordOf(ctx).set(message{
					method:          method,
					tool:            tool,
					protocolVersion: protocolVersion,
					clientName:      clientName,
					clientVersion:   clientVersion,
					outcome:         outcome,
					err:             errorText(res, err),
				})
			}()
			if method == methodSubscriptionsListen {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "subscriptions/listen is not supported: the tools never change"}
			}
			// "arguments": null, which the SDK's own client sends for a nil map, is no arguments: the SDK (v1.8.0
			// applySchema) would panic filling the defaults of the input schema into it
			if call, ok := req.(*sdkmcp.CallToolRequest); ok && call.Params != nil {
				if a := bytes.TrimSpace(call.Params.Arguments); len(a) == 0 || string(a) == "null" {
					call.Params.Arguments = json.RawMessage("{}")
				}
				call.Params.Arguments = r.quoteNumbers(call.Params.Name, call.Params.Arguments)
			}
			return next(ctx, method, req)
		}
	}
}

// outcomeOf classifies the answer to a message (see the observability.MCPOutcome constants).
func outcomeOf(res sdkmcp.Result, err error) string {
	if err != nil {
		return observability.MCPOutcomeError
	}
	// a failed call may leave a nil *CallToolResult in res
	if r, ok := res.(*sdkmcp.CallToolResult); ok && r != nil && r.IsError {
		if errors.Is(r.GetError(), errCold) {
			return observability.MCPOutcomeCold
		}
		return observability.MCPOutcomeToolError
	}
	return observability.MCPOutcomeOK
}

// errorText is the error a message was answered with, for the request log: a JSON-RPC error, or the error of a tool
// result; "" for none
func errorText(res sdkmcp.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	if r, ok := res.(*sdkmcp.CallToolResult); ok && r != nil && r.IsError {
		if e := r.GetError(); e != nil {
			return e.Error()
		}
		return "isError"
	}
	return ""
}

// clientOf returns the client that sent a message and the protocol version it uses: the 2025 protocols give the
// client with initialize only (a later message of theirs gets a session made up from the headers of its request,
// with the version alone), the 2026-07-28 one in the _meta of every message.
func clientOf(req sdkmcp.Request, res sdkmcp.Result) (client *sdkmcp.Implementation, protocolVersion string) {
	switch r := req.(type) {
	case *sdkmcp.ServerRequest[*sdkmcp.InitializeParams]:
		if r.Params != nil {
			client, protocolVersion = r.Params.ClientInfo, r.Params.ProtocolVersion
		}
		// the version agreed on rather than the one asked for
		if ir, ok := res.(*sdkmcp.InitializeResult); ok && ir != nil {
			protocolVersion = ir.ProtocolVersion
		}
		return client, protocolVersion
	case interface {
		ClientInfo() *sdkmcp.Implementation
		ProtocolVersion() string
	}:
		return r.ClientInfo(), r.ProtocolVersion()
	}
	return nil, ""
}

// message is what the request log tells of the JSON-RPC message of a request
type message struct {
	method, tool, protocolVersion, clientName, clientVersion, outcome, err string
}

// record keeps the message of a request from the SDK's goroutine that handles it for the request's goroutine, which
// logs it: zerolog's context of the request must not be updated from another goroutine.
type record struct {
	mu  sync.Mutex
	msg *message
	// deadline is the deadline of the request, after which its answer is not sent
	deadline time.Time
}

type recordKey struct{}

func withRecord(ctx context.Context, rec *record) context.Context {
	return context.WithValue(ctx, recordKey{}, rec)
}

// recordOf returns the record of the request of ctx, nil if it has none (a transport other than HTTP)
func recordOf(ctx context.Context) *record {
	rec, _ := ctx.Value(recordKey{}).(*record)
	return rec
}

// expired tells whether the deadline of the request has passed, false for no record or no deadline
func (r *record) expired() bool {
	return r != nil && !r.deadline.IsZero() && !time.Now().Before(r.deadline)
}

func (r *record) set(m message) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.msg = &m
	r.mu.Unlock()
}

// the bounds of the client's strings in the request log
const (
	maxLoggedName  = 128
	maxLoggedError = 256
)

// log adds the message to the request log and the request's span. A request the SDK rejected before handling its
// message (a wrong method, header or body) has none, nor may a notification, which is answered (202) at once.
func (r *record) log(c *fiber.Ctx) {
	r.mu.Lock()
	m := r.msg
	r.mu.Unlock()
	if m == nil {
		return
	}
	flog.UpdateContext(c, func(lc zerolog.Context) zerolog.Context {
		lc = lc.Str("mcp.method", clip(m.method, maxLoggedName)).Str("mcp.outcome", m.outcome)
		for _, f := range []struct{ key, value string }{
			{"mcp.tool", clip(m.tool, maxLoggedName)},
			{"mcp.protocol_version", clip(m.protocolVersion, maxLoggedName)},
			{"mcp.client.name", clip(m.clientName, maxLoggedName)},
			{"mcp.client.version", clip(m.clientVersion, maxLoggedName)},
			{"mcp.error", clip(m.err, maxLoggedError)},
		} {
			if f.value != "" {
				lc = lc.Str(f.key, f.value)
			}
		}
		return lc
	})
	trace.SpanFromContext(c.UserContext()).SetAttributes(
		attribute.String("mcp.method", clip(m.method, maxLoggedName)),
		attribute.String("mcp.tool", clip(m.tool, maxLoggedName)),
		attribute.String("mcp.outcome", m.outcome),
	)
}

// clip cuts s to at most n bytes, at a rune boundary
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// sdkLogger passes the warnings and errors of the SDK to the app's logger, all as warnings: the SDK logs mistakes of
// clients as errors (a method of the 2025 protocols sent with the 2026-07-28 one, a second initialize), which are no
// errors of the backend. Its info logs, one per request as each request is a session of its own, are left out.
var sdkLogger = slog.New(zerologHandler{})

type zerologHandler struct {
	attrs []slog.Attr
}

func (zerologHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h zerologHandler) Handle(_ context.Context, r slog.Record) error {
	evt := log.Warn().
		Str("evt.name", "mcp.sdk").
		Str("sdk.level", r.Level.String())
	add := func(a slog.Attr) bool {
		evt = evt.Str("sdk."+a.Key, a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	evt.Msg(r.Message)
	return nil
}

func (h zerologHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return zerologHandler{attrs: append(slices.Clip(h.attrs), attrs...)}
}

// WithGroup leaves groups out: the SDK uses none
func (h zerologHandler) WithGroup(string) slog.Handler {
	return h
}
