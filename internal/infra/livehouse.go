package infra

import (
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"exusiai.dev/backend-next/internal/config"
	"exusiai.dev/backend-next/internal/model/pb"
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
		log.Info().Msg("infra: livehouse is disabled")
	}

	return nil, nil
}
