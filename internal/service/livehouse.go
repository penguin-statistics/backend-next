package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/model/pb"
	"github.com/penguin-statistics/backend-next/internal/pkg/dstructs"
)

type LiveHouse struct {
	Client pb.ConnectedLiveServiceClient

	q *dstructs.FlQueue[pb.Report]
	t *time.Ticker
}

func NewLiveHouse(client pb.ConnectedLiveServiceClient) *LiveHouse {
	l := &LiveHouse{
		Client: client,
		q:      dstructs.NewFlQueue[pb.Report](),
		t:      time.NewTicker(time.Second * 5),
	}

	go l.worker()

	return l
}

func (l *LiveHouse) worker() {
	for range l.t.C {
		reports := l.q.Flush()
		if len(reports) == 0 {
			continue
		}

		_, err := l.Client.PushReportBatch(context.Background(), &pb.ReportBatchRequest{
			Report: reports,
		})
		if err != nil {
			log.Error().Err(err).Msg("service: livehouse: failed to report")
		}
	}
}

func (l *LiveHouse) PushReport(report *pb.Report) {
	l.q.Push(report)
}
