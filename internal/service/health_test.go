package service

import (
	"bufio"
	"context"
	"database/sql"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// startHangingServer accepts connections and never answers, like a dependency that is stuck.
func startHangingServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return ln.Addr().String()
}

func newHealthTestDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	return db
}

func newHealthTestRedis(t *testing.T, opts *redis.Options) *redis.Client {
	t.Helper()
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestHealthReportsHangingDependenciesWithinTheProbeTimeout(t *testing.T) {
	s := NewHealth(
		newHealthTestDB(t, "postgres://postgres:postgres@"+startHangingServer(t)+"/postgres?sslmode=disable"),
		newHealthTestRedis(t, &redis.Options{Addr: startHangingServer(t)}),
		reconnectingNATS(t),
	)

	start := time.Now()
	got := s.Check(context.Background())
	// the readiness probe times out after 1 s
	if took := time.Since(start); took > 900*time.Millisecond {
		t.Fatalf("took %s, want it within the probe timeout", took)
	}
	want := HealthStatus{Status: HealthDegraded, Postgres: HealthUnreachable, Redis: HealthUnreachable, NATS: "RECONNECTING"}
	if *got != want {
		t.Fatalf("got %+v, want %+v", *got, want)
	}
}

// startRedisPongServer answers every command but HELLO with PONG, like a healthy Redis.
func startRedisPongServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					// each command starts with the length of its argument array, then the length and the name of the
					// command
					if !strings.HasPrefix(line, "*") {
						continue
					}
					if _, err := r.ReadString('\n'); err != nil {
						return
					}
					name, err := r.ReadString('\n')
					if err != nil {
						return
					}
					// HELLO fails like on Redis 5, so the client falls back to RESP2 without the server having to speak RESP3
					reply := "+PONG\r\n"
					if strings.EqualFold(strings.TrimSpace(name), "HELLO") {
						reply = "-ERR unknown command 'HELLO'\r\n"
					}
					if _, err := c.Write([]byte(reply)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestHealthReportsAHealthyDependencyNextToAHangingOne(t *testing.T) {
	s := NewHealth(
		newHealthTestDB(t, "postgres://postgres:postgres@"+startHangingServer(t)+"/postgres?sslmode=disable"),
		newHealthTestRedis(t, &redis.Options{Addr: startRedisPongServer(t)}),
		reconnectingNATS(t),
	)

	// the Redis ping finishes while the database check waits out the timeout, which made the Redis result race
	// against the done context
	for i := 0; i < 10; i++ {
		want := HealthStatus{Status: HealthDegraded, Postgres: HealthUnreachable, Redis: HealthOK, NATS: "RECONNECTING"}
		if got := s.Check(context.Background()); *got != want {
			t.Fatalf("check %d: got %+v, want %+v", i, *got, want)
		}
	}
}

func TestHealthReportsTheNATSConnectionStatus(t *testing.T) {
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	redisURL := os.Getenv("PENGUIN_TEST_REDIS_URL")
	natsURL := os.Getenv("PENGUIN_TEST_NATS_URL")
	if dsn == "" || redisURL == "" || natsURL == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN, PENGUIN_TEST_REDIS_URL or PENGUIN_TEST_NATS_URL is not set")
	}
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	db := newHealthTestDB(t, dsn)
	redisClient := newHealthTestRedis(t, redisOpts)
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	want := HealthStatus{Status: HealthOK, Postgres: HealthOK, Redis: HealthOK, NATS: "CONNECTED"}
	if got := NewHealth(db, redisClient, nc).Check(context.Background()); *got != want {
		t.Fatalf("got %+v, want %+v", *got, want)
	}

	want = HealthStatus{Status: HealthDegraded, Postgres: HealthOK, Redis: HealthOK, NATS: "RECONNECTING"}
	if got := NewHealth(db, redisClient, reconnectingNATS(t)).Check(context.Background()); *got != want {
		t.Fatalf("got %+v, want %+v", *got, want)
	}
}
