package service

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/pb"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/dstructs"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type LiveHouse struct {
	Client    pb.ConnectedLiveServiceClient
	StageRepo *repo.Stage

	q   *dstructs.FlQueue[*pb.Report]
	t   *time.Ticker
	gen uint64
}

func NewLiveHouse(client pb.ConnectedLiveServiceClient, stageRepo *repo.Stage) (*LiveHouse, error) {
	l := &LiveHouse{
		Client:    client,
		StageRepo: stageRepo,
		q:         dstructs.NewFlQueue[*pb.Report](),
		t:         time.NewTicker(time.Second * 5),
	}

	if err := l.checkConfig(); err != nil {
		return nil, err
	}

	go l.worker()

	return l, nil
}

func (l *LiveHouse) checkConfig() error {
	if l.Client == nil {
		return errors.New("service: livehouse: client is nil. is livehouse enabled?")
	}
	if l.StageRepo == nil {
		return errors.New("service: livehouse: stage repo is nil")
	}

	return nil
}

func (l *LiveHouse) worker() {
	for range l.t.C {
		reports := l.q.Flush()
		if len(reports) == 0 {
			continue
		}

		_, err := l.Client.PushReportBatch(context.Background(), &pb.ReportBatchRequest{
			Reports: reports,
		})
		if err != nil {
			log.Error().Err(err).Msg("service: livehouse: failed to report")
		}
	}
}

func (l *LiveHouse) PushReport(r *types.ReportTaskSingleReport, stageId uint32, server string) error {
	var pbserv pb.Server
	if m, ok := constant.ServerIDMapping[server]; ok {
		pbserv = pb.Server(m)
	} else {
		return errors.New("service/livehouse: invalid server")
	}

	pr := &pb.Report{
		Server:     pbserv,
		Generation: atomic.LoadUint64(&l.gen),
		StageId:    stageId,
		Drops:      make([]*pb.Drop, 0, len(r.Drops)),
	}
	for _, d := range r.Drops {
		pr.Drops = append(pr.Drops, &pb.Drop{
			ItemId:   uint32(d.ItemID),
			Quantity: uint64(d.Quantity),
		})
	}
	l.q.Push(pr)

	return nil
}

func (l *LiveHouse) PushMatrix() {
	atomic.AddUint64(&l.gen, 1)
}
