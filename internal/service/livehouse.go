package service

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/config"
	"exusiai.dev/backend-next/internal/constant"
	"exusiai.dev/backend-next/internal/model/pb"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/dstructs"
	"exusiai.dev/backend-next/internal/repo"
)

type LiveHouse struct {
	Enabled   bool
	Client    pb.ConnectedLiveServiceClient
	StageRepo *repo.Stage

	q   *dstructs.FlQueue[*pb.Report]
	t   *time.Ticker
	gen uint64
}

func NewLiveHouse(client pb.ConnectedLiveServiceClient, stageRepo *repo.Stage, conf *config.Config) (*LiveHouse, error) {
	l := &LiveHouse{
		Enabled:   conf.LiveHouseEnabled,
		Client:    client,
		StageRepo: stageRepo,
		q:         dstructs.NewFlQueue[*pb.Report](),
		t:         time.NewTicker(time.Second * 5),
	}

	if l.Enabled {
		if err := l.checkConfig(); err != nil {
			return nil, err
		}

		go l.worker()
	} else {
		log.Info().
			Str("evt.name", "livehouse.disabled").
			Msg("service: livehouse: disabled")
	}

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
			log.Error().
				Str("evt.name", "livehouse.report.failed").
				Err(err).
				Msg("failed to push report batch")
		} else {
			log.Info().
				Str("evt.name", "livehouse.report.success").
				Int("count", len(reports)).
				Msg("successfully reported reports to livehouse")
		}
	}
}

func (l *LiveHouse) PushReport(r *types.ReportTaskSingleReport, stageId uint32, server string) error {
	if !l.Enabled {
		return nil
	}

	var pbserv pb.Server
	if m, ok := constant.ServerIDMapping[server]; ok {
		pbserv = pb.Server(m)
	} else {
		return errors.New("service: livehouse: unknown server")
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
	if !l.Enabled {
		return
	}
	atomic.AddUint64(&l.gen, 1)
}
