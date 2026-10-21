package middlewares

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// newConcurrencyLimitedApp serves GET /block, which blocks until release is closed, and GET /fail, which returns an
// error, both behind ConcurrencyLimit(max, wait). started receives a value whenever a request reaches a handler.
func newConcurrencyLimitedApp(max int, wait time.Duration) (app *fiber.App, started chan struct{}, release chan struct{}) {
	started = make(chan struct{}, 16)
	release = make(chan struct{})
	app = fiber.New()
	app.Use(ConcurrencyLimit(max, wait, func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusTooManyRequests)
	}))
	app.Get("/block", func(c *fiber.Ctx) error {
		started <- struct{}{}
		<-release
		return c.SendStatus(fiber.StatusOK)
	})
	app.Get("/fail", func(c *fiber.Ctx) error {
		started <- struct{}{}
		return fiber.ErrInternalServerError
	})
	return app, started, release
}

// get requests path in the background and sends the response status, or 0 on error, to the returned channel.
func get(app *fiber.App, path string) <-chan int {
	status := make(chan int, 1)
	go func() {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil), -1)
		if err != nil {
			status <- 0
			return
		}
		status <- resp.StatusCode
	}()
	return status
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the handler")
	}
}

func expectStatus(t *testing.T, status <-chan int, want int) {
	t.Helper()
	select {
	case got := <-status:
		if got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no response, want status %d", want)
	}
}

func TestConcurrencyLimitRejectsAfterWaiting(t *testing.T) {
	const wait = 100 * time.Millisecond
	app, started, release := newConcurrencyLimitedApp(2, wait)

	first, second := get(app, "/block"), get(app, "/block")
	waitStarted(t, started)
	waitStarted(t, started)

	begin := time.Now()
	expectStatus(t, get(app, "/block"), fiber.StatusTooManyRequests)
	if elapsed := time.Since(begin); elapsed < wait {
		t.Errorf("rejected after %v, want after waiting %v", elapsed, wait)
	}
	select {
	case <-started:
		t.Error("a rejected request reached the handler")
	default:
	}

	close(release)
	expectStatus(t, first, fiber.StatusOK)
	expectStatus(t, second, fiber.StatusOK)
}

func TestConcurrencyLimitWaitingRequestGetsFreedSlot(t *testing.T) {
	app, started, release := newConcurrencyLimitedApp(1, 5*time.Second)

	first := get(app, "/block")
	waitStarted(t, started)
	second := get(app, "/block")
	select {
	case <-started:
		t.Fatal("a second request ran while the only slot was taken")
	case <-time.After(100 * time.Millisecond):
	}

	// the first request finishes and the waiting one takes its slot
	release <- struct{}{}
	expectStatus(t, first, fiber.StatusOK)
	waitStarted(t, started)
	close(release)
	expectStatus(t, second, fiber.StatusOK)
}

func TestConcurrencyLimitReleasesSlotOnError(t *testing.T) {
	app, started, release := newConcurrencyLimitedApp(1, 100*time.Millisecond)
	defer close(release)

	for i := 0; i < 3; i++ {
		expectStatus(t, get(app, "/fail"), fiber.StatusInternalServerError)
		waitStarted(t, started)
	}
}
