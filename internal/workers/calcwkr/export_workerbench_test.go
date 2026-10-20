//go:build workerbench

package calcwkr

import (
	"context"
	"time"

	"github.com/go-redsync/redsync/v4"
)

// Exposes one worker batch to the external workerbench harness.

// NewWorkerForBench builds a worker like Start does, without the batch timer and with no separation sleeps.
func NewWorkerForBench(deps WorkerDeps) *Worker {
	return &Worker{
		timeout:    deps.Config.WorkerTimeout,
		syncMutex:  deps.RedSync.NewMutex("mutex:calcwkr", redsync.WithExpiry(30*time.Second), redsync.WithTries(2)),
		WorkerDeps: deps,
	}
}

// RunStatsBatchForBench runs exactly what one stats batch of the worker runs, holding the worker lock.
func (w *Worker) RunStatsBatchForBench(ctx context.Context) error {
	if err := w.lock(); err != nil {
		return err
	}
	defer w.unlock()
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	return runForAllServers(ctx, w.calcStats)
}
