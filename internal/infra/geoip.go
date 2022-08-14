package infra

import (
	_ "embed"

	"github.com/oschwald/geoip2-golang"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/geoip"
)

func GeoIPDatabase(conf *config.Config) (*geoip2.Reader, error) {
	db, err := geoip2.FromBytes(geoip.Database)
	if err != nil {
		return nil, err
	}

	return db, nil
}
