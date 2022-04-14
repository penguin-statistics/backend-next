package geoip

import (
	"net"

	"github.com/oschwald/geoip2-golang"
	"github.com/pkg/errors"
)

type Service struct {
	db *geoip2.Reader
}

func NewService(db *geoip2.Reader) *Service {
	return &Service{
		db: db,
	}
}

func (s *Service) Country(ip string) (*geoip2.Country, error) {
	netIP := net.ParseIP(ip)
	if netIP == nil {
		return nil, errors.New("invalid ip")
	}
	return s.db.Country(netIP)
}

func (s *Service) InChinaMainland(ip string) bool {
	country, err := s.Country(ip)
	if err != nil || country == nil {
		return false
	}
	return country.Country.IsoCode == "CN"
}
