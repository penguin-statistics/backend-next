package infra

import (
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/model/pb"
)

func LiveHouse(conf *config.Config) (pb.ConnectedLiveServiceClient, error) {
	if conf.LiveHouseEnabled {
		conn, err := grpc.Dial(conf.LiveHouseGRPCAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("infra: failed to connect to livehouse")
			return nil, err
		}

		return pb.NewConnectedLiveServiceClient(conn), nil
	} else {
		log.Warn().Msg("infra: livehouse is disabled")
	}

	return nil, nil
}
