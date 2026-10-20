package infra

import (
	"testing"
	"time"
)

func TestPostgresConnectorReadTimeout(t *testing.T) {
	for _, tc := range []struct {
		dsn  string
		want time.Duration
	}{
		{"postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable", postgresReadTimeout},
		{"postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable&timeout=1200", 1200 * time.Second},
		{"postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable&read_timeout=30s", 30 * time.Second},
	} {
		if got := postgresConnector(tc.dsn).Config().ReadTimeout; got != tc.want {
			t.Errorf("read timeout of %s is %v, want %v", tc.dsn, got, tc.want)
		}
	}
}
