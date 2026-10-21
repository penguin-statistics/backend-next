//go:build linux || darwin

package middlewares

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// lockedBuffer is a bytes.Buffer the server goroutines can log to while the
// test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// waitRequestLog waits for the single "http.request" log line in b.
func waitRequestLog(t *testing.T, b *lockedBuffer) map[string]any {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		logged := bytes.Contains(b.buf.Bytes(), []byte(`"evt.name":"http.request"`))
		snapshot := bytes.NewBuffer(append([]byte(nil), b.buf.Bytes()...))
		b.mu.Unlock()
		if logged || time.Now().After(deadline) {
			return requestLog(t, snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// serveLoggedApp serves app on a real TCP listener, unlike app.Test, so that
// the request logger sees a socket it can inspect, and returns its address.
func serveLoggedApp(t *testing.T, app *fiber.App) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return ln.Addr().String()
}

func TestRequestLoggerLogsPeerClose(t *testing.T) {
	tests := []struct {
		name string
		// whether the client resets the connection instead of closing it
		reset bool
		// how long the client waits before going away
		wait    time.Duration
		handler func(ctx *fiber.Ctx) error
		level   string
	}{
		{"close then 200", false, 0, func(ctx *fiber.Ctx) error { return ctx.SendString("ok") }, "warn"},
		{"reset then 200", true, 0, func(ctx *fiber.Ctx) error { return ctx.SendString("ok") }, "warn"},
		{"close then 302", false, 0, func(ctx *fiber.Ctx) error { return ctx.Redirect("/elsewhere") }, "warn"},
		{"close then 500", false, 0, func(ctx *fiber.Ctx) error { return errors.New("boom") }, "error"},
		{"close past the read timeout", false, 2 * testReadTimeout, func(ctx *fiber.Ctx) error { return ctx.SendString("ok") }, "warn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf lockedBuffer
			app, _ := newLoggedApp(t, &buf)

			entered, gone := make(chan struct{}), make(chan struct{})
			app.Get("/slow", func(ctx *fiber.Ctx) error {
				close(entered)
				// as a slow handler, finish only once the client has gone away,
				// without looking at the connection: the first peek after a RST
				// returns ECONNRESET and clears it, and in production the request
				// logger is the only one that peeks
				<-gone
				time.Sleep(50 * time.Millisecond)
				return tt.handler(ctx)
			})
			addr := serveLoggedApp(t, app)

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Write([]byte("GET /slow HTTP/1.1\r\nHost: test\r\n\r\n")); err != nil {
				t.Fatal(err)
			}
			<-entered
			time.Sleep(tt.wait)
			if tt.reset {
				if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			close(gone)

			entry := waitRequestLog(t, &buf)
			if got := entry["http.peer_closed"]; got != true {
				t.Errorf("logged http.peer_closed = %v, want true", got)
			}
			if got := entry["level"]; got != tt.level {
				t.Errorf("logged level = %v, want %s", got, tt.level)
			}
			// the line keeps the fields every request log has
			if got := entry["http.route"]; got != "/slow" {
				t.Errorf("logged http.route = %v, want /slow", got)
			}
		})
	}
}

func TestRequestLoggerDoesNotFlagDeliveredRequests(t *testing.T) {
	var buf lockedBuffer
	app, _ := newLoggedApp(t, &buf)
	app.Get("/", func(ctx *fiber.Ctx) error { return ctx.SendString("ok") })
	app.Get("/slow", func(ctx *fiber.Ctx) error {
		// handled past the read deadline, while the client keeps waiting: the
		// failing Read of a connection like this one must not count as closed
		time.Sleep(2 * testReadTimeout)
		return ctx.SendString("ok")
	})
	addr := serveLoggedApp(t, app)

	// requests on one keep-alive connection, which the client keeps open
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
	defer client.CloseIdleConnections()
	for i, path := range []string{"/", "/", "/slow"} {
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		entry := waitRequestLog(t, &buf)
		if got, ok := entry["http.peer_closed"]; ok {
			t.Errorf("request %d: logged http.peer_closed = %v, want no field", i, got)
		}
		if got := entry["level"]; got != "info" {
			t.Errorf("request %d: logged level = %v, want info", i, got)
		}
		buf.mu.Lock()
		buf.buf.Reset()
		buf.mu.Unlock()
	}
}
