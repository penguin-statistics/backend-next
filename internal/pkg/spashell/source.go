package spashell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/singleflight"

	"exusiai.dev/backend-next/internal/pkg/bininfo"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// MaxShellSize is the largest index.html accepted. The app's is about 25 KB.
const MaxShellSize = 2 << 20

// ErrUnavailable is returned by Get when no shell has been fetched and fetching one failed.
var ErrUnavailable = errors.New("spashell: no shell fetched yet")

const (
	// DefaultTTL is the TTL of a Config without a positive one
	DefaultTTL = 15 * time.Second
	// DefaultTimeout is the Timeout of a Config without a positive one
	DefaultTimeout = 3 * time.Second
)

// ValidateURL tells whether raw is an absolute http(s) URL with a host, which a Source can fetch.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("spashell: %q is not an absolute http(s) URL", raw)
	}
	return nil
}

// Config configures a Source.
type Config struct {
	// URL is the URL of the index.html
	URL string
	// TTL is how often the shell is fetched again; DefaultTTL if not positive
	TTL time.Duration
	// Timeout is the timeout of a single fetch; DefaultTimeout if not positive
	Timeout time.Duration
	// RetryAfter is how long Get answers ErrUnavailable without fetching after a fetch without a copy at hand
	// failed; 10s by default
	RetryAfter time.Duration
	// Client is the HTTP client fetches use; by default one with Timeout that ignores the proxy settings of the
	// environment (the shell is fetched in the cluster) and follows no redirects: a redirect is a failed fetch, so
	// only the configured URL can supply the HTML served as the site's own
	Client *http.Client
}

// newClient is the default Client of a Config
func newClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Source keeps a copy of the app's index.html: it fetches it again every TTL, revalidating the copy at hand, and
// keeps the copy at hand when a fetch fails. Requests are served from the copy at hand without waiting for a fetch;
// only with no copy at all does Get fetch one.
type Source struct {
	conf Config
	cur  atomic.Pointer[Shell]
	sf   singleflight.Group

	// lastFailure is when the last fetch without a copy at hand failed, in Unix nanoseconds
	lastFailure atomic.Int64
	// lastAttempt is when the last fetch started, in Unix nanoseconds
	lastAttempt atomic.Int64

	now func() time.Time
}

// New returns a Source of conf; a TTL or Timeout that is not positive is replaced by its default.
func New(conf Config) *Source {
	if conf.TTL <= 0 {
		conf.TTL = DefaultTTL
	}
	if conf.Timeout <= 0 {
		conf.Timeout = DefaultTimeout
	}
	if conf.RetryAfter <= 0 {
		conf.RetryAfter = 10 * time.Second
	}
	if conf.Client == nil {
		conf.Client = newClient(conf.Timeout)
	}
	return &Source{conf: conf, now: time.Now}
}

// Config is the configuration of s, with the defaults applied.
func (s *Source) Config() Config {
	return s.conf
}

// Get returns the copy at hand. With no copy, it fetches one unless a fetch failed less than RetryAfter ago; then, or
// if the fetch fails, it returns ErrUnavailable. A copy older than twice the TTL, which Run should have revalidated,
// is revalidated in the background, at most once every RetryAfter.
func (s *Source) Get(ctx context.Context) (*Shell, error) {
	if cur := s.cur.Load(); cur != nil {
		now := s.now()
		if last := s.lastAttempt.Load(); now.Sub(cur.FetchedAt) > 2*s.conf.TTL && now.Sub(time.Unix(0, last)) >= s.conf.RetryAfter &&
			s.lastAttempt.CompareAndSwap(last, now.UnixNano()) {
			go func() { _ = s.Refresh(context.Background()) }()
		}
		return cur, nil
	}
	if last := s.lastFailure.Load(); last != 0 && s.now().Sub(time.Unix(0, last)) < s.conf.RetryAfter {
		return nil, ErrUnavailable
	}
	if err := s.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if cur := s.cur.Load(); cur != nil {
		return cur, nil
	}
	return nil, ErrUnavailable
}

// Refresh fetches the shell, or revalidates the copy at hand, once at a time: a call while a fetch is in flight
// waits for that fetch. The copy at hand is kept if the fetch fails.
func (s *Source) Refresh(ctx context.Context) error {
	ch := s.sf.DoChan("fetch", func() (any, error) {
		// not cancelled with the context of the first caller: the others wait for the fetch as well
		fctx, cancel := context.WithTimeout(context.Background(), s.conf.Timeout)
		defer cancel()
		return nil, s.fetch(fctx)
	})
	select {
	case res := <-ch:
		return res.Err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run fetches the shell, then fetches it again every TTL until ctx is done.
func (s *Source) Run(ctx context.Context) {
	if s.cur.Load() == nil {
		// the age of the shell counts from now until one is fetched
		observability.SEOShellFresh(s.now())
	}
	_ = s.Refresh(ctx)
	ticker := time.NewTicker(s.conf.TTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Refresh(ctx)
		}
	}
}

func (s *Source) fetch(ctx context.Context) (err error) {
	s.lastAttempt.Store(s.now().UnixNano())
	cur := s.cur.Load()
	outcome := "error"
	defer func() {
		observability.SEOShellFetchTotal.WithLabelValues(outcome).Inc()
		if err != nil {
			if cur == nil {
				s.lastFailure.Store(s.now().UnixNano())
			}
			log.Warn().
				Str("evt.name", "seo.shell.fetch_failed").
				Err(err).
				Str("url", s.conf.URL).
				Bool("hasCopy", cur != nil).
				Msg("failed to fetch the index.html of the frontend; serving the copy at hand if any")
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.conf.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "PenguinBackend/"+bininfo.Version+" (+https://penguin-stats.io; index.html for stats pages)")
	if cur != nil {
		if cur.etag != "" {
			req.Header.Set("If-None-Match", cur.etag)
		}
		if cur.lastModifiedText != "" {
			req.Header.Set("If-Modified-Since", cur.lastModifiedText)
		}
	}

	resp, err := s.conf.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && cur != nil {
		outcome = "not_modified"
		now := s.now()
		s.cur.CompareAndSwap(cur, cur.revalidated(now))
		observability.SEOShellFresh(now)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		if loc := resp.Header.Get("Location"); loc != "" {
			return fmt.Errorf("unexpected status %d (redirect to %q, not followed)", resp.StatusCode, loc)
		}
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "text/html" {
		return fmt.Errorf("unexpected Content-Type %q", resp.Header.Get("Content-Type"))
	}
	doc, err := io.ReadAll(io.LimitReader(resp.Body, MaxShellSize+1))
	if err != nil {
		return err
	}
	if len(doc) > MaxShellSize {
		return fmt.Errorf("index.html larger than %d bytes", MaxShellSize)
	}
	shell, err := Parse(doc)
	if err != nil {
		return err
	}
	now := s.now()
	shell.FetchedAt = now
	shell.etag = resp.Header.Get("ETag")
	shell.lastModifiedText = resp.Header.Get("Last-Modified")
	shell.LastModified = now
	if t, err := http.ParseTime(shell.lastModifiedText); err == nil {
		shell.LastModified = t
	}
	// a different index.html is never older than the one at hand, as that of a release rolled back to is: the pages
	// take their Last-Modified from it, and one cached with the newer one would be answered as not modified while it
	// refers to the scripts of the release rolled back from. Later by a second at least, the precision of HTTP dates.
	if cur != nil && !bytes.Equal(cur.raw, shell.raw) && !shell.LastModified.After(cur.LastModified) {
		shell.LastModified = now
		if floor := cur.LastModified.Add(time.Second); shell.LastModified.Before(floor) {
			shell.LastModified = floor
		}
	}
	if !shell.Spliceable() {
		log.Warn().
			Str("evt.name", "seo.shell.unspliceable").
			Str("url", s.conf.URL).
			Msg("the index.html of the frontend lacks <html>, </head> or <main id=\"app\">...</main>: serving it as is")
	}

	outcome = "ok"
	if !shell.Spliceable() {
		outcome = "ok_unspliceable"
	}
	s.cur.Store(shell)
	observability.SEOShellFresh(now)
	return nil
}
