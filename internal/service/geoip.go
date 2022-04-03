package service

import (
	"net"

	"github.com/oschwald/geoip2-golang"
	"github.com/pkg/errors"
)

type GeoIP struct {
	db *geoip2.Reader
}

func NewGeoIP(db *geoip2.Reader) *GeoIP {
	return &GeoIP{
		db: db,
	}
}

func (s *GeoIP) Country(ip string) (*geoip2.Country, error) {
	netIP := net.ParseIP(ip)
	if netIP == nil {
		return nil, errors.New("invalid ip")
	}
	return s.db.Country(netIP)
}

func (s *GeoIP) InChinaMainland(ip string) bool {
	country, err := s.Country(ip)
	if err != nil || country == nil {
		return false
	}
	return country.Country.IsoCode == "CN"
}
