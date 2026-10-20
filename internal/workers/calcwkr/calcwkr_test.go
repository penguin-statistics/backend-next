package calcwkr

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
)

func TestRunForAllServersDoesNotInterruptRunningServers(t *testing.T) {
	if serverConcurrency != 2 || len(constant.Servers) < 4 || constant.Servers[0] != "CN" {
		t.Skip("test assumes two concurrent servers and CN first")
	}
	errFailed := errors.New("failed")
	var (
		mu      sync.Mutex
		started []string
	)
	peerStarted := make(chan struct{})
	peerFinished := make(chan error, 1)

	err := runForAllServers(context.Background(), func(ctx context.Context, server string) error {
		mu.Lock()
		started = append(started, server)
		mu.Unlock()
		switch server {
		case "CN":
			// fail while the other server is running
			<-peerStarted
			return errFailed
		case constant.Servers[1]:
			close(peerStarted)
			// CN's failure must not cancel this server halfway
			time.Sleep(50 * time.Millisecond)
			peerFinished <- ctx.Err()
		}
		return nil
	})

	if !errors.Is(err, errFailed) {
		t.Fatalf("got error %v, want %v", err, errFailed)
	}
	if ctxErr := <-peerFinished; ctxErr != nil {
		t.Fatalf("the server running alongside the failed one was interrupted: %v", ctxErr)
	}
	if len(started) != 2 {
		t.Fatalf("servers started after the failure: %v", started)
	}
}
