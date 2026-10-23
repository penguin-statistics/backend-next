package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util"
)

// requestTimeout is how long the SDK waits for the answer of a request; past it, it drops the answer and the client
// gets a 503 to retry (see serve). It ends a request the SDK keeps open waiting on the context (a subscriptions/listen,
// which observe rejects), as fasthttp never cancels a request whose client went away. It does not make a slow tool
// answer sooner: the SDK gives handlers a context that is never cancelled and waits for them when it closes the
// session, so the 503 goes out once the tool returned, and the tool holds its worker and its slot of the concurrency
// limit until then. The tools only read memory: it is a backstop.
var requestTimeout = 15 * time.Second

// maxBodyBytes bounds the body of a request (413 past it): a tool call is a few hundred bytes
const maxBodyBytes = 64 << 10

// maxConcurrent requests are handled at the same time by a replica; a request finding them all busy waits up to
// concurrencyWait for one to finish
const (
	maxConcurrent   = 32
	concurrencyWait = time.Second
)

// the rate limit with a setting that is not positive
const (
	defaultRateLimitMax    = 1200
	defaultRateLimitWindow = time.Minute
)

// Register serves the MCP server at /mcp if it is enabled, and its server card and its guide if it has a public URL
// (MCPPublicURL), which browsers opening /mcp are then sent to. Disabled, /mcp is not routed: 404, and neither are the
// card and the guide. A public URL that is not one fails the startup, enabled or not, rather than be found out when
// the server is turned on.
func Register(app *fiber.App, conf *appconfig.Config, data *service.SEOData) error {
	public, err := parsePublicURL(conf.MCPPublicURL)
	if err != nil {
		return err
	}
	if !conf.MCPEnabled {
		return nil
	}
	limit, window := rateLimitOf(conf.MCPRateLimitMax, conf.MCPRateLimitWindow)
	if public != nil {
		// before the server, whose limits it is ahead of
		mountEndpointHelp(app, guideURL(public))
	}
	Mount(app, NewServer(data, conf.SEOSiteURL, bininfo.Version), limit, window)
	if public != nil {
		if err := mountServerCard(app, newServerCard(public, conf.SEOSiteURL, bininfo.Version)); err != nil {
			return err
		}
		if err := mountGuide(app, public, conf.SEOSiteURL, limit, window); err != nil {
			return err
		}
	}
	log.Info().
		Str("evt.name", "mcp.enabled").
		Int("rateLimitMax", limit).
		Dur("rateLimitWindow", window).
		Str("siteURL", conf.SEOSiteURL).
		Str("publicURL", conf.MCPPublicURL).
		Msg("serving the MCP server at " + middlewares.MCPPath)
	return nil
}

// publicURLPattern is the form of a public URL: https, a host name in lower case, maybe a port, and the path /mcp with
// nothing after it. The card and the guide give it to clients and people as it is, in links, commands and JSON, so it
// must be the one URL of the endpoint, and a plain one.
var publicURLPattern = regexp.MustCompile(`^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?/mcp$`)

// parsePublicURL parses the public URL of the server (MCPPublicURL), nil for none.
func parsePublicURL(s string) (*url.URL, error) {
	if s == "" {
		return nil, nil
	}
	if !publicURLPattern.MatchString(s) {
		return nil, fmt.Errorf("PENGUIN_V3_MCP_PUBLIC_URL %q is not the URL of the MCP endpoint: it must be https://<host>/mcp "+
			"(e.g. https://mcp.penguin-stats.io/mcp), with the host in lower case and nothing after /mcp", s)
	}
	return url.Parse(s)
}

// Mount routes /mcp (middlewares.MCPPath) to srv behind a rate limit per client (rateLimitMax requests per
// rateLimitWindow, in fixed windows) and a limit of the requests handled at once. Both limits count HTTP requests, so
// serve refuses JSON-RPC batches: a request carries one message.
//
// The MCP specification has servers check the Origin of requests, against DNS rebinding of a server on the
// visitor's own machine. This one is public, read-only and needs no credentials: it accepts every Origin, and
// middlewares.CORS answers every Origin in the public tier for this path.
func Mount(r fiber.Router, srv *sdkmcp.Server, rateLimitMax int, rateLimitWindow time.Duration) {
	h := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, &sdkmcp.StreamableHTTPOptions{
		// every request is a session of its own: any replica answers any request, and GET (an event stream for the
		// server's messages) and DELETE (the end of a session) are answered 405
		Stateless: true,
		// one JSON response rather than an event stream: fasthttp's adaptor sends the response only once the
		// handler returned
		JSONResponse:        true,
		MaxRequestBodyBytes: maxBodyBytes,
		Logger:              sdkLogger,
	})
	r.All(middlewares.MCPPath,
		rateLimit(rateLimitMax, rateLimitWindow),
		middlewares.ConcurrencyLimit(maxConcurrent, concurrencyWait, busy),
		serve(h))
}

// serve hands a request to h, the SDK's net/http handler, with the context of the request (its logger, span and
// request ID, which fiber's adaptor leaves out) bounded by requestTimeout, then adds what the SDK handled to the
// request log.
func serve(h http.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// a batch of messages (the 2025-03-26 protocol, which the SDK assumes for a request without
		// Mcp-Protocol-Version) would run all its calls at once for one request of the rate and concurrency limits; no
		// client of the newer protocols sends one. The raw body is what the SDK would read: fasthttp does not
		// decompress it.
		if c.Method() == fiber.MethodPost {
			if b := bytes.TrimLeft(c.Request().Body(), " \t\r\n"); len(b) > 0 && b[0] == '[' {
				return sendError(c, fiber.StatusBadRequest, jsonrpc.CodeInvalidRequest,
					"JSON-RPC batches are not supported: send one message per request")
			}
		}
		ctx, cancel := context.WithTimeout(c.UserContext(), requestTimeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		rec := &record{deadline: deadline}
		ctx = withRecord(ctx, rec)
		fasthttpadaptor.NewFastHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(ctx))
		}))(c.Context())
		switch {
		case c.Response().StatusCode() == fiber.StatusMethodNotAllowed:
			// GET and DELETE: there is no event stream nor session to give (see Mount)
			middlewares.ExpectStatus(c)
		case errors.Is(ctx.Err(), context.DeadlineExceeded) && c.Response().StatusCode() == fiber.StatusOK && len(c.Response().Body()) == 0:
			// the SDK stopped waiting for the answer at the deadline, having set the headers of a JSON response only
			// (observe counted the message as timed out)
			c.Set(fiber.HeaderRetryAfter, "1")
			_ = sendErrorTo(c, requestID(c.Request().Body()), fiber.StatusServiceUnavailable, codeServerError, "timed out, retry")
		}
		// the plugins of penguin-statistics/agent-skills add ?via=plugin to the URL of the server, to count their
		// requests by a field of its own rather than by the raw query of http.url, which clients fill as they like
		if c.Query("via") == "plugin" {
			flog.UpdateContext(c, func(lc zerolog.Context) zerolog.Context { return lc.Str("mcp.via", "plugin") })
		}
		rec.log(c)
		return nil
	}
}

// rateLimit limits the requests of each client (see clientKey) to limit per window, in fixed windows. A request past it
// is answered 429 with the seconds until the window resets in Retry-After, which fiber's limiter sets: a client that
// waits that long is served.
func rateLimit(limit int, window time.Duration) fiber.Handler {
	limit, window = rateLimitOf(limit, window)
	return limiter.New(limiter.Config{
		Max:               limit,
		Expiration:        window,
		LimiterMiddleware: limiter.FixedWindow{},
		KeyGenerator:      clientKey,
		LimitReached: func(c *fiber.Ctx) error {
			return sendError(c, fiber.StatusTooManyRequests, codeServerError, "rate limited, retry after "+c.GetRespHeader(fiber.HeaderRetryAfter)+"s")
		},
	})
}

// rateLimitOf returns the rate limit of the settings, with the default for a setting that is not positive.
func rateLimitOf(limit int, window time.Duration) (int, time.Duration) {
	if limit <= 0 {
		limit = defaultRateLimitMax
	}
	if window <= 0 {
		window = defaultRateLimitWindow
	}
	return limit, window
}

// busy answers a request that found every slot of the concurrency limit taken.
func busy(c *fiber.Ctx) error {
	c.Set(fiber.HeaderRetryAfter, "1")
	return sendError(c, fiber.StatusTooManyRequests, codeServerError, "too many requests at once, retry after 1s")
}

// clientKey is the client of a request for the rate limit: its IP (util.ExtractIP: from the proxy header of a request
// through the ingress), or the address of its peer without one. An IPv6 address counts as its /64, which a host
// usually has whole.
func clientKey(c *fiber.Ctx) string {
	ip, err := netip.ParseAddr(util.ExtractIP(c))
	if err != nil {
		peer, ok := netip.AddrFromSlice(c.Context().RemoteIP())
		if !ok {
			return ""
		}
		ip = peer
	}
	ip = ip.Unmap()
	if ip.Is6() {
		prefix, _ := ip.Prefix(64)
		return prefix.String()
	}
	return ip.String()
}

// rpcError is a JSON-RPC error response sent by serve rather than the SDK
type rpcError struct {
	JSONRPC string `json:"jsonrpc"`
	// ID is that of the request, or null if it was not read
	ID    any `json:"id"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// codeServerError is the JSON-RPC code of an error of the server, outside the codes of the specification
const codeServerError = -32000

func sendError(c *fiber.Ctx, status, code int, message string) error {
	return sendErrorTo(c, nil, status, code, message)
}

// sendErrorTo answers the request of ID id (nil: null) with an error.
func sendErrorTo(c *fiber.Ctx, id json.RawMessage, status, code int, message string) error {
	body := rpcError{JSONRPC: "2.0"}
	if id != nil {
		body.ID = id
	}
	body.Error.Code, body.Error.Message = code, message
	return c.Status(status).JSON(body)
}

// requestID is the ID of the JSON-RPC request in body, or nil if it has none or is not one.
func requestID(body []byte) json.RawMessage {
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(body, &msg) != nil || len(msg.ID) == 0 || bytes.Equal(msg.ID, []byte("null")) {
		return nil
	}
	return msg.ID
}
