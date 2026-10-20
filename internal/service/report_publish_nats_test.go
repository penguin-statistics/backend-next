package service

// Tests of the report task publisher against a real NATS server with JetStream. They are skipped unless
// PENGUIN_TEST_NATS_URL points at one, see internal/workers/reportwkr/consumer_nats_test.go. Every test works on
// subjects of its own, and deletes the stream it creates.

import (
	"context"
	"math/rand"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/infra"
	"exusiai.dev/backend-next/internal/model/types"
)

// newTestPublisher connects to PENGUIN_TEST_NATS_URL with the options of the app and returns a Report publishing with
// it, and a prefix of subjects no other test uses.
func newTestPublisher(t *testing.T) (s *Report, nc *nats.Conn, prefix string) {
	t.Helper()
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	nc, err := nats.Connect(url, infra.NATSOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	return &Report{NatsConn: nc, NatsJS: js}, nc, "P" + strconv.FormatUint(rand.Uint64(), 36)
}

// addTestStream creates a stream of the subjects prefix.*, like penguin-reports in production, deleted after the test.
func addTestStream(t *testing.T, s *Report, prefix string) (name string) {
	t.Helper()
	name = "TEST_" + prefix
	if _, err := s.NatsJS.AddStream(&nats.StreamConfig{
		Name:       name,
		Subjects:   []string{prefix + ".*"},
		Retention:  nats.WorkQueuePolicy,
		Storage:    nats.FileStorage,
		Duplicates: time.Minute * 10,
		// a stream left behind by a killed run expires on its own
		MaxAge: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.NatsJS.DeleteStream(name); err != nil {
			t.Error(err)
		}
	})
	return name
}

// commitInRequest runs commitReportTask in a request handler, after setup, as the report controller does.
func commitInRequest(t *testing.T, s *Report, subject string, setup func(c *fiber.Ctx)) (taskId string, err error) {
	t.Helper()
	return commitTaskInRequest(t, s, subject, &types.ReportTask{AccountID: 1}, setup)
}

// commitTaskInRequest is commitInRequest of task.
func commitTaskInRequest(t *testing.T, s *Report, subject string, task *types.ReportTask, setup func(c *fiber.Ctx)) (taskId string, err error) {
	t.Helper()
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		c.Locals(constant.ContextKeyRequestID, "req")
		if setup != nil {
			setup(c)
		}
		taskId, err = s.commitReportTask(c, subject, task)
		return nil
	})
	if _, testErr := app.Test(httptest.NewRequest("POST", "/", nil), -1); testErr != nil {
		t.Fatal(testErr)
	}
	return taskId, err
}

// silenceSubject subscribes to subject without ever answering, like a stream whose server does not respond.
func silenceSubject(t *testing.T, nc *nats.Conn, subject string) {
	t.Helper()
	if _, err := nc.Subscribe(subject, func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestCommitReportTaskPublishes(t *testing.T) {
	s, _, prefix := newTestPublisher(t)
	stream := addTestStream(t, s, prefix)

	taskId, err := commitInRequest(t, s, prefix+".SINGLE", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(taskId, "req-") {
		t.Errorf("taskId = %q, want the request id and a suffix", taskId)
	}

	msg, err := s.NatsJS.GetLastMsg(stream, prefix+".SINGLE")
	if err != nil {
		t.Fatal(err)
	}
	var task types.ReportTask
	if err := json.Unmarshal(msg.Data, &task); err != nil {
		t.Fatal(err)
	}
	if task.TaskID != taskId || task.AccountID != 1 {
		t.Errorf("stored task = %+v, want taskId %q and accountId 1", task, taskId)
	}
}

func TestCommitReportTaskWithoutStream(t *testing.T) {
	s, _, prefix := newTestPublisher(t)

	start := time.Now()
	_, err := commitInRequest(t, s, prefix+".SINGLE", nil)
	if !errors.Is(err, nats.ErrNoStreamResponse) {
		t.Errorf("got %v, want %v", err, nats.ErrNoStreamResponse)
	}
	// the client retries a request no one answers twice, 250ms apart
	if elapsed := time.Since(start); elapsed > time.Second*5 {
		t.Errorf("failed after %v, want well within the 10s bound", elapsed)
	}
}

func TestCommitReportTaskTimesOut(t *testing.T) {
	s, nc, prefix := newTestPublisher(t)
	silenceSubject(t, nc, prefix+".SINGLE")

	start := time.Now()
	_, err := commitInRequest(t, s, prefix+".SINGLE", nil)
	if !errors.Is(err, ErrNatsTimeout) {
		t.Errorf("got %v, want ErrNatsTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second*10 || elapsed > time.Second*12 {
		t.Errorf("timed out after %v, want 10s", elapsed)
	}
	// an ack given up on and left pending blocks every publish once such acks reach the limit
	if n := s.NatsJS.PublishAsyncPending(); n != 0 {
		t.Errorf("%d acks pending after the publish was given up on, want 0", n)
	}
}

func TestCommitReportTaskCanceled(t *testing.T) {
	s, nc, prefix := newTestPublisher(t)
	silenceSubject(t, nc, prefix+".SINGLE")

	// the client goes away while the task is published
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond*200, cancel)
	defer cancel()

	start := time.Now()
	_, err := commitInRequest(t, s, prefix+".SINGLE", func(c *fiber.Ctx) {
		c.SetUserContext(ctx)
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want %v", err, context.Canceled)
	}
	if elapsed := time.Since(start); elapsed > time.Second*5 {
		t.Errorf("returned after %v, want soon after the cancellation at 200ms", elapsed)
	}
	if n := s.NatsJS.PublishAsyncPending(); n != 0 {
		t.Errorf("%d acks pending after the publish was given up on, want 0", n)
	}
}

// TestCommitReportTaskLeavesNoRequestState publishes tasks whose send fails at once (over the maximum payload) and a
// task whose client goes away: no response entry or subscription of the connection is left behind. With the default
// request style, the client (v1.24.0) keeps the response entry of each send that failed at once.
func TestCommitReportTaskLeavesNoRequestState(t *testing.T) {
	s, nc, prefix := newTestPublisher(t)
	addTestStream(t, s, prefix)
	silenceSubject(t, nc, prefix+"_SILENT.SINGLE")
	// a publish that succeeds first, so that whatever the connection keeps for every request already exists
	if _, err := commitInRequest(t, s, prefix+".SINGLE", nil); err != nil {
		t.Fatal(err)
	}
	subs := nc.NumSubscriptions()

	big := &types.ReportTask{AccountID: 1, IP: strings.Repeat("x", int(nc.MaxPayload()))}
	for i := 0; i < 5; i++ {
		if _, err := commitTaskInRequest(t, s, prefix+".SINGLE", big, nil); !errors.Is(err, nats.ErrMaxPayload) {
			t.Fatalf("got %v, want %v", err, nats.ErrMaxPayload)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()
	if _, err := commitInRequest(t, s, prefix+"_SILENT.SINGLE", func(c *fiber.Ctx) { c.SetUserContext(ctx) }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want %v", err, context.Canceled)
	}

	// the response entries of the connection, read as the client does not export them; nothing else writes them
	// here, as no response is on its way
	if n := reflect.ValueOf(nc).Elem().FieldByName("respMap").Len(); n != 0 {
		t.Errorf("%d response entries left in the connection, want 0", n)
	}
	if n := nc.NumSubscriptions(); n != subs {
		t.Errorf("%d subscriptions, want the %d before", n, subs)
	}
}

func TestCommitReportTaskDeduplicatesByIdempotencyKey(t *testing.T) {
	s, _, prefix := newTestPublisher(t)
	stream := addTestStream(t, s, prefix)

	withKey := func(key string) func(c *fiber.Ctx) {
		return func(c *fiber.Ctx) {
			// as the idempotency middleware saves the X-Penguin-Idempotency-Key of the request
			c.Locals(constant.IdempotencyKeyLocalsKey, key)
		}
	}
	// a request retried with its key (after a timeout, say), another key, and two requests of no key
	for _, setup := range []func(c *fiber.Ctx){withKey("key1"), withKey("key1"), withKey("key2"), nil, nil} {
		if _, err := commitInRequest(t, s, prefix+".SINGLE", setup); err != nil {
			t.Fatal(err)
		}
	}

	info, err := s.NatsJS.StreamInfo(stream)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 4 {
		t.Errorf("stream holds %d tasks, want 4: one per key, and each of no key", info.State.Msgs)
	}
}
