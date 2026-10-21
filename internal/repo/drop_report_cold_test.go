package repo

import (
	"reflect"
	"testing"
	"time"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

func TestSplitWholeHours(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.DateTime, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	r := func(start, end string) coldstore.TimeRange {
		return coldstore.TimeRange{Start: at(start), End: at(end)}
	}
	for _, tc := range []struct {
		name        string
		thresholds  []string
		raw, rollup []coldstore.TimeRange
	}{
		{
			name:       "whole hours",
			thresholds: []string{"2019-05-01 04:00:00", "2019-05-02 04:00:00"},
			rollup:     []coldstore.TimeRange{r("2019-05-01 04:00:00", "2019-05-02 04:00:00")},
		},
		{
			name:       "partial first and last hours",
			thresholds: []string{"2019-05-01 04:17:23", "2019-05-01 08:42:10"},
			raw:        []coldstore.TimeRange{r("2019-05-01 04:17:23", "2019-05-01 05:00:00"), r("2019-05-01 08:00:00", "2019-05-01 08:42:10")},
			rollup:     []coldstore.TimeRange{r("2019-05-01 05:00:00", "2019-05-01 08:00:00")},
		},
		{
			name:       "within an hour",
			thresholds: []string{"2019-05-01 04:05:00", "2019-05-01 04:55:00"},
			raw:        []coldstore.TimeRange{r("2019-05-01 04:05:00", "2019-05-01 04:55:00")},
		},
		{
			name:       "across one hour boundary only",
			thresholds: []string{"2019-05-01 04:05:00", "2019-05-01 05:55:00"},
			raw:        []coldstore.TimeRange{r("2019-05-01 04:05:00", "2019-05-01 05:55:00")},
		},
		{
			// the hour cut by the boundary at 06:30 is read from the reports; the ranges of the segments are merged
			name:       "segments",
			thresholds: []string{"2019-05-01 04:00:00", "2019-05-01 06:30:00", "2019-05-01 08:00:00", "2019-05-01 10:00:00"},
			raw:        []coldstore.TimeRange{r("2019-05-01 06:00:00", "2019-05-01 07:00:00")},
			rollup:     []coldstore.TimeRange{r("2019-05-01 04:00:00", "2019-05-01 06:00:00"), r("2019-05-01 07:00:00", "2019-05-01 10:00:00")},
		},
	} {
		var thresholds []time.Time
		for _, s := range tc.thresholds {
			thresholds = append(thresholds, at(s))
		}
		raw, rollup := splitWholeHours(thresholds)
		if !reflect.DeepEqual(raw, tc.raw) || !reflect.DeepEqual(rollup, tc.rollup) {
			t.Errorf("%s: raw %v, rollup %v; want %v, %v", tc.name, raw, rollup, tc.raw, tc.rollup)
		}
	}
}

func TestTimestamptz(t *testing.T) {
	if got := timestamptz(time.Date(2019, 5, 1, 4, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))); got != "TIMESTAMPTZ '2019-04-30 20:00:00+00'" {
		t.Errorf("got %s", got)
	}
	if got := timestamptz(time.Date(2019, 5, 1, 4, 0, 1, 500000000, time.UTC)); got != "TIMESTAMPTZ '2019-05-01 04:00:01.5+00'" {
		t.Errorf("got %s", got)
	}
}

func TestNewColdDropReport(t *testing.T) {
	conf := &appconfig.Config{}
	if r, err := NewColdDropReport(conf, nil); r != nil || err != nil {
		t.Errorf("disabled: %v, %v", r, err)
	}
	conf.ColdstoreQueriesEnabled = true
	if _, err := NewColdDropReport(conf, nil); err == nil {
		t.Error("enabled without the cold storage: no error")
	}
}
