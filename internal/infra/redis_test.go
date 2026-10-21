package infra

import (
	"bufio"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// closedAddr returns an address nothing listens on (yet).
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

func TestPingRedisGivesUpAfterTheTimeout(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: closedAddr(t)})
	t.Cleanup(func() { client.Close() })

	start := time.Now()
	if err := pingRedis(client, 2*time.Second); err == nil {
		t.Fatal("got no error from an address nothing listens on")
	}
	// pings at 0 s, 0.5 s and 1.5 s; the next one would come after the timeout
	if took := time.Since(start); took < time.Second || took > 3*time.Second {
		t.Fatalf("took %s, want about 1.5 s", took)
	}
}

// startRedisProxyAfter forwards addr to the Redis at target, but only starts listening after delay, like a Redis that
// is still being rescheduled when the backend boots.
func startRedisProxyAfter(t *testing.T, addr, target string, delay time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var ln net.Listener
	var conns []net.Conn
	track := func(c net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		conns = append(conns, c)
	}
	timer := time.AfterFunc(delay, func() {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		ln = l
		mu.Unlock()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			track(c)
			up, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			track(up)
			go func() { _, _ = io.Copy(up, c) }()
			go func() { _, _ = io.Copy(c, up) }()
		}
	})
	t.Cleanup(func() {
		timer.Stop()
		mu.Lock()
		defer mu.Unlock()
		if ln != nil {
			ln.Close()
		}
		for _, c := range conns {
			c.Close()
		}
	})
}

func TestRedisWaitsForRedisToComeUpAtBoot(t *testing.T) {
	redisURL := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	u, err := url.Parse(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	target := u.Host
	u.Host = closedAddr(t)
	startRedisProxyAfter(t, u.Host, target, 1500*time.Millisecond)

	start := time.Now()
	client, err := Redis(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{RedisURL: u.String()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if took := time.Since(start); took < 1500*time.Millisecond {
		t.Fatalf("took %s, want it to have waited for the proxy", took)
	}
}

// startRedisErrorServer answers every command with the error reply, like a Redis that is loading its dataset or
// rejects the password.
func startRedisErrorServer(t *testing.T, reply string) string {
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
					// each command starts with the length of its argument array
					if strings.HasPrefix(line, "*") {
						if _, err := c.Write([]byte("-" + reply + "\r\n")); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestPingRedisRetriesOnlyWhileRedisIsLoading(t *testing.T) {
	loading := redis.NewClient(&redis.Options{Addr: startRedisErrorServer(t, "LOADING Redis is loading the dataset in memory")})
	t.Cleanup(func() { loading.Close() })
	start := time.Now()
	if err := pingRedis(loading, 2*time.Second); err == nil || !strings.HasPrefix(err.Error(), "LOADING ") {
		t.Fatalf("got %v, want the LOADING error", err)
	}
	if took := time.Since(start); took < time.Second {
		t.Fatalf("took %s, want it to have retried until the timeout", took)
	}

	wrongPass := redis.NewClient(&redis.Options{Addr: startRedisErrorServer(t, "WRONGPASS invalid username-password pair or user is disabled.")})
	t.Cleanup(func() { wrongPass.Close() })
	start = time.Now()
	if err := pingRedis(wrongPass, 10*time.Second); err == nil || !strings.HasPrefix(err.Error(), "WRONGPASS ") {
		t.Fatalf("got %v, want the WRONGPASS error", err)
	}
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Fatalf("took %s, want it to fail without retrying", took)
	}
}
