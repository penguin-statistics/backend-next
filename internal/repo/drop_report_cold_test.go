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
	// the reader is only kept, never used, here
	reader := new(coldstore.Reader)
	if r, err := NewColdDropReport(conf, reader); err != nil || r.Shadow().Enabled {
		t.Errorf("queries enabled: %v, %v", r, err)
	}

	conf.ColdstoreShadowEnabled = true
	conf.ColdstoreShadowTimeout, conf.ColdstoreShadowMaxInFlight = 30*time.Second, 2
	if _, err := NewColdDropReport(conf, reader); err == nil {
		t.Error("queries and shadow mode enabled: no error")
	}
	conf.ColdstoreQueriesEnabled = false
	if _, err := NewColdDropReport(conf, nil); err == nil {
		t.Error("shadow mode without the cold storage: no error")
	}
	r, err := NewColdDropReport(conf, reader)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ColdShadowSettings{Enabled: true, Timeout: 30 * time.Second, MaxInFlight: 2}); r.Shadow() != want {
		t.Errorf("shadow settings %+v, want %+v", r.Shadow(), want)
	}
	conf.ColdstoreShadowMaxInFlight = 0
	if _, err := NewColdDropReport(conf, reader); err == nil {
		t.Error("shadow mode without slots: no error")
	}
}

func TestNewColdTrendPlan(t *testing.T) {
	utc := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.DateTime, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	boundary := utc("2019-07-01 00:00:00")
	// the CN game day of 2019-06-28 12:00 UTC starts at 2019-06-27 20:00 UTC: 31 buckets of 5 hours, the 16th of
	// them, [06-30 23:00, 07-01 04:00), straddling the boundary
	start := utc("2019-06-28 12:00:00")
	type bucket struct {
		groupId    int
		start, end string
	}
	for _, tc := range []struct {
		name       string
		from, to   time.Time
		thresholds []string
		buckets    []bucket
	}{
		{
			name:       "cut to the first buckets",
			from:       utc("2019-01-01 00:00:00"),
			to:         utc("2019-06-28 03:00:00"),
			thresholds: []string{"2019-06-27 20:00:00", "2019-06-28 01:00:00", "2019-06-28 03:00:00"},
			buckets:    []bucket{{0, "2019-06-27 20:00:00", "2019-06-28 01:00:00"}, {1, "2019-06-28 01:00:00", "2019-06-28 06:00:00"}},
		},
		{
			name:       "the straddling bucket",
			from:       utc("2019-06-30 18:00:00.5"),
			to:         boundary,
			thresholds: []string{"2019-06-30 18:00:00", "2019-06-30 23:00:00", "2019-07-01 00:00:00"},
			buckets:    []bucket{{14, "2019-06-30 18:00:00", "2019-06-30 23:00:00"}, {15, "2019-06-30 23:00:00", "2019-07-01 04:00:00"}},
		},
		{
			name: "empty",
			from: boundary,
			to:   boundary,
		},
	} {
		plan, err := newColdTrendPlan(boundary, tc.from, tc.to, "CN", start, 5*time.Hour, 30, []int{1})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		// the cold storage computes the segments from the first bucket
		if len(plan.buckets) > 0 && (!plan.plan.bucketStart.Equal(plan.buckets[0].start) || plan.plan.bucketLength != 5*time.Hour) {
			t.Errorf("%s: buckets from %s of %s", tc.name, plan.plan.bucketStart, plan.plan.bucketLength)
		}
		var thresholds []string
		for _, th := range plan.plan.thresholds {
			thresholds = append(thresholds, th.UTC().Format(time.DateTime))
		}
		var buckets []bucket
		for _, b := range plan.buckets {
			buckets = append(buckets, bucket{b.groupId, b.start.UTC().Format(time.DateTime), b.end.UTC().Format(time.DateTime)})
		}
		if !reflect.DeepEqual(thresholds, tc.thresholds) || !reflect.DeepEqual(buckets, tc.buckets) {
			t.Errorf("%s: thresholds %v, buckets %v; want %v, %v", tc.name, thresholds, buckets, tc.thresholds, tc.buckets)
		}
	}

	// the whole cold part: 16 buckets, the last one cut at the boundary
	plan, err := newColdTrendPlan(boundary, time.Time{}, boundary, "CN", start, 5*time.Hour, 30, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(plan.buckets); n != 16 || len(plan.plan.thresholds) != 17 || plan.buckets[n-1].groupId != 15 || !plan.plan.thresholds[16].Equal(boundary) {
		t.Errorf("up to the boundary: thresholds %v, buckets %v", plan.plan.thresholds, plan.buckets)
	}
	if _, err := newColdTrendPlan(boundary, start, boundary.Add(time.Second), "CN", start, 5*time.Hour, 30, []int{1}); err == nil {
		t.Error("a trend past the hot boundary: no error")
	}
	if _, err := newColdTrendPlan(boundary, start, boundary, "CN", start, 30*time.Minute, 30, []int{1}); err == nil {
		t.Error("an interval length shorter than an hour: no error")
	}
}
