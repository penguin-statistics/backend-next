package reportwkr

// A test of the report consumer losing its connection to NATS, which it reaches through a proxy of the test. Skipped
// unless PENGUIN_TEST_NATS_URL is set, like the tests of consumer_nats_test.go.

import (
	"context"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"exusiai.dev/backend-next/internal/model/types"
)

// natsProxy forwards TCP connections to the NATS server, so that a test can cut its own connection without stopping
// the server: cut closes the connections and refuses new ones until restore. pause holds what the clients send, the
// connections staying up, until resume.
type natsProxy struct {
	ln     net.Listener
	target string

	mu     sync.Mutex
	down   bool
	conns  []net.Conn
	paused chan struct{} // closed by resume; nil while not paused
}

func newNATSProxy(t *testing.T, natsURL string) *natsProxy {
	t.Helper()
	u, err := url.Parse(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &natsProxy{ln: ln, target: u.Host}
	t.Cleanup(func() {
		_ = ln.Close()
		p.resume()
		p.cut()
	})
	go p.serve()
	return p
}

func (p *natsProxy) url() string { return "nats://" + p.ln.Addr().String() }

func (p *natsProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		var server net.Conn
		if !p.down {
			server, err = net.Dial("tcp", p.target)
		}
		if p.down || err != nil {
			p.mu.Unlock()
			_ = client.Close()
			continue
		}
		p.conns = append(p.conns, client, server)
		p.mu.Unlock()
		pipe := func(dst net.Conn, w io.Writer, src net.Conn) {
			// a plain writer: io.Copy would hand src to dst.ReadFrom, which bypasses a pausableWriter
			_, _ = io.Copy(struct{ io.Writer }{w}, src)
			_ = dst.Close()
			_ = src.Close()
		}
		go pipe(server, &pausableWriter{p: p, w: server}, client)
		go pipe(client, client, server)
	}
}

// pausableWriter writes what a client sends to the server, waiting while the proxy is paused.
type pausableWriter struct {
	p *natsProxy
	w io.Writer
}

func (w *pausableWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	paused := w.p.paused
	w.p.mu.Unlock()
	if paused != nil {
		<-paused
	}
	return w.w.Write(b)
}

func (p *natsProxy) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused == nil {
		p.paused = make(chan struct{})
	}
}

func (p *natsProxy) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused != nil {
		close(p.paused)
		p.paused = nil
	}
}

func (p *natsProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = true
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *natsProxy) restore() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = false
}

// TestPullConsumerFetchesNothingWhileDisconnected cuts the connection of a worker for 40 times its fetchWait while
// tasks are queued. A fetch while the connection is down is buffered by the client, so a worker fetching meanwhile
// would send 40 requests on reconnect and get every task at once, most of them waiting in the client past their
// AckWait to be delivered again. The worker waits for the connection instead: it holds at most 2 tasks, and each task
// is delivered and handled once.
func TestPullConsumerFetchesNothingWhileDisconnected(t *testing.T) {
	consumeThroughInterruption(t, func(p *natsProxy, nc *nats.Conn, wait time.Duration, publish func()) {
		p.cut()
		for deadline := time.Now().Add(5 * time.Second); !nc.IsReconnecting(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("connection not lost")
			}
		}
		publish()
		time.Sleep(wait)
		p.restore()
	})
}

// TestPullConsumerFetchesOnceWhileTrafficStalls holds what a worker sends for 40 times its fetchWait while tasks are
// queued, the connection staying up. Each fetch meanwhile times out in the client while its request waits on the way,
// so a worker fetching meanwhile would have 40 requests reach the server at once when the traffic resumes, and get
// every task at once. The worker waits until its requests have arrived instead: it holds at most 2 tasks, and each
// task is delivered and handled once.
func TestPullConsumerFetchesOnceWhileTrafficStalls(t *testing.T) {
	consumeThroughInterruption(t, func(p *natsProxy, nc *nats.Conn, wait time.Duration, publish func()) {
		p.pause()
		publish()
		time.Sleep(wait)
		if !nc.IsConnected() {
			t.Fatal("connection lost while paused")
		}
		p.resume()
	})
}

// consumeThroughInterruption runs a worker connected through a proxy, with a fetchWait of 25ms and a handler that
// takes 250ms. interrupt disrupts the connection, publishes 20 tasks (5s of handling, more than the AckWait of 3s)
// with publish, waits for wait (40 fetchWaits), and ends the disruption. The test then checks that each task is
// delivered and handled once, and that the worker holds at most 2 tasks at once.
func consumeThroughInterruption(t *testing.T, interrupt func(p *natsProxy, nc *nats.Conn, wait time.Duration, publish func())) {
	t.Helper()
	s := newTestStream(t)
	p := newNATSProxy(t, s.url)
	nc, err := nats.Connect(p.url(), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	conf := s.consumerConfig()
	conf.workers = 1
	conf.fetchWait = 25 * time.Millisecond
	const published = 20

	var handled handledCounter
	w := newWorker(WorkerDeps{NatsJS: js, NatsConn: nc}, conf)
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		time.Sleep(250 * time.Millisecond)
		handled.add(task.TaskID)
		return nil
	}
	startConsumer(t, w)

	interrupt(p, nc, 40*conf.fetchWait, func() { s.publishTasks(t, published) })

	maxAckPending, maxRedelivered := 0, 0
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		ci := s.consumerInfo(t, conf.durable)
		if ci.NumAckPending > maxAckPending {
			maxAckPending = ci.NumAckPending
		}
		if ci.NumRedelivered > maxRedelivered {
			maxRedelivered = ci.NumRedelivered
		}
		si, err := s.js.StreamInfo(s.name)
		if err != nil {
			t.Fatal(err)
		}
		if si.State.Msgs == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream still holds %d messages", si.State.Msgs)
		}
	}

	distinct, total, repeated := handled.stats()
	t.Logf("%d tasks published, %d handled %d times, up to %d held at once", published, distinct, total, maxAckPending)
	if distinct != published || total != published || repeated != 0 {
		t.Errorf("%d tasks handled %d times, %d of them more than once; want each of %d once", distinct, total, repeated, published)
	}
	if maxAckPending > 2 {
		t.Errorf("up to %d tasks held by the worker at once, want at most 2", maxAckPending)
	}
	if maxRedelivered != 0 {
		t.Errorf("up to %d tasks redelivered at once, want none", maxRedelivered)
	}
	if ci := s.consumerInfo(t, conf.durable); ci.Delivered.Consumer != published {
		t.Errorf("%d deliveries, want %d", ci.Delivered.Consumer, published)
	}
}
