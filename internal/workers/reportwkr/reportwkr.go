package reportwkr

import (
	"context"
	"github.com/davecgh/go-spew/spew"
	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/service"
	"go.uber.org/fx"
	"runtime"
)

type WorkerDeps struct {
	fx.In
	ReportServices *service.Report
}

type Worker struct {
	// count is the number of workers
	count int

	WorkerDeps
}

func Start(conf *config.Config, deps WorkerDeps) {
	ch := make(chan error)
	// handle & dump errors from workers
	go func() {
		for {
			err := <-ch
			spew.Dump(err)
		}
	}()
	// works like a consumer factory
	reportWorkers := &Worker{
		count:      0,
		WorkerDeps: deps,
	}
	// spawn workers
	// maybe we should specify the number of worker in config.Config ?
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			err := reportWorkers.ReportServices.ReportConsumeWorker(context.Background(), ch)
			if err != nil {
				ch <- err
			}
		}()
		// update current worker count
		reportWorkers.count += 1
	}
}
