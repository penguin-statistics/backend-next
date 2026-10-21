package spashell

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type upstream struct {
	mu          sync.Mutex
	status      int
	contentType string
	body        string
	etag        string
	block       chan struct{}

	hits         atomic.Int32
	ifNoneMatchs []string
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.hits.Add(1)
	if u.block != nil {
		<-u.block
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ifNoneMatchs = append(u.ifNoneMatchs, r.Header.Get("If-None-Match"))
	if u.etag != "" && r.Header.Get("If-None-Match") == u.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", u.contentType)
	if u.etag != "" {
		w.Header().Set("ETag", u.etag)
	}
	w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 09:09:28 GMT")
	w.WriteHeader(u.status)
	_, _ = w.Write([]byte(u.body))
}

func (u *upstream) set(status int, body, etag string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status, u.body, u.etag = status, body, etag
}

const (
	shellV1 = `<html lang="zh"><head><title>t</title></head><body><main id="app">v1</main></body></html>`
	shellV2 = `<html lang="zh"><head><title>t</title></head><body><main id="app">v2</main></body></html>`
)

type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time          { return time.Unix(0, c.t.Load()) }
func (c *clock) advance(d time.Duration) { c.t.Add(int64(d)) }

func newSource(t *testing.T, u *upstream) (*Source, *clock) {
	t.Helper()
	srv := httptest.NewServer(u)
	t.Cleanup(srv.Close)
	s := New(Config{URL: srv.URL + "/", TTL: time.Minute, Timeout: 2 * time.Second, RetryAfter: 10 * time.Second})
	c := &clock{}
	c.t.Store(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).UnixNano())
	s.now = c.now
	return s, c
}

func mustGet(t *testing.T, s *Source) *Shell {
	t.Helper()
	sh, err := s.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestSourceFetchesOnceAndRevalidates(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html; charset=utf-8", body: shellV1, etag: `"v1"`}
	s, c := newSource(t, u)

	sh := mustGet(t, s)
	if !strings.Contains(string(sh.Raw()), "v1") || !sh.Spliceable() {
		t.Fatalf("first shell: %q", sh.Raw())
	}
	if want := time.Date(2026, 10, 21, 9, 9, 28, 0, time.UTC); !sh.LastModified.Equal(want) {
		t.Errorf("LastModified %v, want %v", sh.LastModified, want)
	}
	// served from the copy at hand without fetching
	for i := 0; i < 5; i++ {
		mustGet(t, s)
	}
	if n := u.hits.Load(); n != 1 {
		t.Fatalf("%d fetches, want 1", n)
	}

	// revalidated unchanged: same document, newer FetchedAt
	c.advance(time.Minute)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := u.ifNoneMatchs[len(u.ifNoneMatchs)-1]; got != `"v1"` {
		t.Errorf("If-None-Match %q", got)
	}
	re := mustGet(t, s)
	if string(re.Raw()) != string(sh.Raw()) || !re.FetchedAt.Equal(c.now()) {
		t.Errorf("after 304: %q fetched at %v", re.Raw(), re.FetchedAt)
	}

	// a new release is picked up by the next fetch
	u.set(200, shellV2, `"v2"`)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustGet(t, s).Raw()), "v2") {
		t.Error("new release not picked up")
	}
}

func TestSourceKeepsLastGoodCopy(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	s, _ := newSource(t, u)
	mustGet(t, s)

	for name, set := range map[string]func(){
		"500": func() { u.set(500, "oops", "") },
		"not html": func() {
			u.mu.Lock()
			u.status, u.body, u.contentType = 200, `{"x":1}`, "application/json"
			u.mu.Unlock()
		},
		"not shell": func() {
			u.mu.Lock()
			u.status, u.body, u.contentType = 200, `<html><body>challenge</body></html>`, "text/html"
			u.mu.Unlock()
		},
		"too large": func() {
			u.mu.Lock()
			u.status, u.contentType, u.body = 200, "text/html", `<main id="app"></main>`+strings.Repeat("x", MaxShellSize)
			u.mu.Unlock()
		},
	} {
		set()
		if err := s.Refresh(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
		if got := mustGet(t, s); !strings.Contains(string(got.Raw()), "v1") {
			t.Errorf("%s: copy at hand replaced by %q", name, got.Raw())
		}
	}
}

func TestSourceUnavailable(t *testing.T) {
	u := &upstream{status: 502, contentType: "text/html", body: "bad gateway"}
	s, c := newSource(t, u)

	if _, err := s.Get(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get: %v, want ErrUnavailable", err)
	}
	// not fetched again until RetryAfter has passed
	for i := 0; i < 3; i++ {
		if _, err := s.Get(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Get: %v, want ErrUnavailable", err)
		}
	}
	if n := u.hits.Load(); n != 1 {
		t.Fatalf("%d fetches within RetryAfter, want 1", n)
	}

	u.mu.Lock()
	u.status, u.body = 200, shellV1
	u.mu.Unlock()
	c.advance(11 * time.Second)
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") {
		t.Errorf("after recovery: %q", sh.Raw())
	}
}

func TestSourceFetchesOnceAtATime(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html", body: shellV1, block: make(chan struct{})}
	s, _ := newSource(t, u)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Get(context.Background())
			errs <- err
		}()
	}
	// let the requests pile up on the fetch in flight
	deadline := time.Now().Add(2 * time.Second)
	for u.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(u.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := u.hits.Load(); n != 1 {
		t.Errorf("%d fetches for 20 concurrent requests, want 1", n)
	}
}

func TestSourceRevalidatesStaleCopyInBackground(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	s, c := newSource(t, u)
	mustGet(t, s)

	u.set(200, shellV2, "")
	c.advance(90 * time.Second) // within twice the TTL: Run revalidates it
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") || u.hits.Load() != 1 {
		t.Fatalf("fetched again within twice the TTL")
	}
	c.advance(time.Minute)
	// served the stale copy right away, revalidated in the background
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") {
		t.Fatalf("got %q, want the stale copy", sh.Raw())
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(string(mustGet(t, s).Raw()), "v2") {
		if time.Now().After(deadline) {
			t.Fatal("stale copy not revalidated")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSourceThrottlesBackgroundRevalidation(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	s, c := newSource(t, u)
	mustGet(t, s)

	// the frontend fails, and Run is not revalidating: requests trigger a fetch every RetryAfter at most
	u.set(500, "oops", "")
	c.advance(3 * time.Minute)
	for i := 0; i < 50; i++ {
		mustGet(t, s)
	}
	waitHits := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for u.hits.Load() < want && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		if n := u.hits.Load(); n != want {
			t.Fatalf("%d fetches, want %d", n, want)
		}
	}
	waitHits(2)
	c.advance(5 * time.Second)
	mustGet(t, s)
	waitHits(2)
	c.advance(6 * time.Second)
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") {
		t.Fatal("copy at hand dropped")
	}
	waitHits(3)
}

func TestSourceRunStopsWithContext(t *testing.T) {
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	s, _ := newSource(t, u)
	s.conf.TTL = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for u.hits.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if u.hits.Load() < 3 {
		t.Errorf("%d fetches, want Run to fetch every TTL", u.hits.Load())
	}
}

func TestSourceDoesNotFollowRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(&upstream{status: 200, contentType: "text/html", body: shellV2})
	t.Cleanup(elsewhere.Close)
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	s, c := newSource(t, u)
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") {
		t.Fatal("first copy not fetched")
	}
	redirect := httptest.NewServer(http.RedirectHandler(elsewhere.URL+"/", http.StatusFound))
	t.Cleanup(redirect.Close)
	s.conf.URL = redirect.URL + "/"
	c.advance(time.Hour)
	if err := s.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "not followed") {
		t.Fatalf("Refresh: %v, want the redirect refused", err)
	}
	if sh := mustGet(t, s); !strings.Contains(string(sh.Raw()), "v1") {
		t.Error("the copy at hand was replaced by the redirect target")
	}
}

func TestSourceIgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	u := &upstream{status: 200, contentType: "text/html", body: shellV1}
	srv := httptest.NewServer(u)
	t.Cleanup(srv.Close)
	// httptest listens on 127.0.0.1, which ProxyFromEnvironment never proxies: check the transport instead
	s := New(Config{URL: srv.URL + "/"})
	if tr, ok := s.conf.Client.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Error("the default client uses a proxy from the environment")
	}
	if _, err := s.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		conf := New(Config{URL: "http://frontend/", TTL: d, Timeout: d}).Config()
		if conf.TTL != DefaultTTL || conf.Timeout != DefaultTimeout || conf.Client.Timeout != DefaultTimeout {
			t.Errorf("TTL and Timeout %v: got %v, %v", d, conf.TTL, conf.Timeout)
		}
	}
	// Run with a TTL of 0 used to panic in NewTicker
	s := New(Config{URL: "http://127.0.0.1:1/", TTL: 0, Timeout: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	<-done
}

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://frontend-rollout.default.svc.cluster.local/": true,
		"https://penguin-stats.io/index.html":                true,
		"":                                                   false,
		"frontend-rollout/":                                  false,
		"file:///etc/passwd":                                 false,
		"ftp://frontend/":                                    false,
		"http:///index.html":                                 false,
	} {
		if err := ValidateURL(raw); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v", raw, err)
		}
	}
}
