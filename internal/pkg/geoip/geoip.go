package geoip

import _ "embed"

//go:embed data/GeoLite2-Country.mmdb
var Database []byte
