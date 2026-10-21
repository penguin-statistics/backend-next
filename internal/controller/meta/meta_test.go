package meta

import (
	"database/sql"
	"io"
	"net"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

// closedAddr returns an address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// The health endpoint is the readiness probe of the only replica: it must stay 200 while the dependencies are down.
func TestHealthStaysOKWhileDependenciesAreDown(t *testing.T) {
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://postgres:postgres@"+closedAddr(t)+"/postgres?sslmode=disable"))), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	redisClient := redis.NewClient(&redis.Options{Addr: closedAddr(t)})
	t.Cleanup(func() { redisClient.Close() })
	// with RetryOnFailedConnect, Connect returns a connection that keeps reconnecting in the background
	nc, err := nats.Connect("nats://"+closedAddr(t), nats.RetryOnFailedConnect(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	app := fiber.New()
	RegisterMeta(&svr.Meta{Router: app.Group("/api/_")}, Meta{HealthService: service.NewHealth(db, redisClient, nc)})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/api/_/health", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code is %d, want 200", resp.StatusCode)
	}
	want := `{"status":"degraded","postgres":"unreachable","redis":"unreachable","nats":"RECONNECTING"}`
	if string(body) != want {
		t.Fatalf("body is %s, want %s", body, want)
	}
}
