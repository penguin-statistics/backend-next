package reportwkr

import (
	"context"
	"github.com/davecgh/go-spew/spew"
	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/service"
	"go.uber.org/fx"
	"runtime"
)

type ReportConsumerDeps struct {
	fx.In
	ReportServices *service.Report
}

type ReportConsumerWorker struct {
	// count is the number of workers
	numWorker int

	ReportConsumerDeps
}

func Start(conf *config.Config, deps ReportConsumerDeps) {
	ch := make(chan error)
	// handle & dump errors from workers
	go func() {
		for {
			err := <-ch
			spew.Dump(err)
		}
	}()
	// works like a consumer factory
	reportWorkers := &ReportConsumerWorker{
		numWorker:          0,
		ReportConsumerDeps: deps,
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
		reportWorkers.numWorker += 1
	}
}
