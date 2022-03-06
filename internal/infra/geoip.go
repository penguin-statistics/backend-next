package infra

import (
	"github.com/oschwald/geoip2-golang"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func GeoIPDatabase(conf *config.Config) (*geoip2.Reader, error) {
	db, err := geoip2.Open(conf.GeoIPDBPath)
	if err != nil {
		return nil, err
	}

	return db, nil
}
