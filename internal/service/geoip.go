package service

import (
	"net"

	"github.com/oschwald/geoip2-golang"
	"github.com/pkg/errors"
)

type GeoIPService struct {
	db *geoip2.Reader
}

func NewGeoIPService(db *geoip2.Reader) *GeoIPService {
	return &GeoIPService{
		db: db,
	}
}

func (s *GeoIPService) Country(ip string) (*geoip2.Country, error) {
	netIP := net.ParseIP(ip)
	if netIP == nil {
		return nil, errors.New("invalid ip")
	}
	return s.db.Country(netIP)
}

func (s *GeoIPService) InChinaMainland(ip string) bool {
	country, err := s.Country(ip)
	if err != nil || country == nil {
		return false
	}
	return country.Country.IsoCode == "CN"
}
