// Package coldstore archives the historical drop reports to Parquet files (the cold storage, #752): the layout of
// the files, their export from PostgreSQL, the manifest that indexes them and their verification.
//
// A cold storage directory (the coldstore/v1 prefix of the bucket, or a local mirror of it) holds:
//
//	manifest.json                                  see Manifest
//	dims/drop_pattern_elements.parquet             the drop patterns' items, to join the pattern_id of reports
//	drop_reports/drop_reports_YYYY-MM.parquet      non-recruit reports, ORDER BY server, account_id, created_at
//	drop_report_extras/extras_YYYY-MM.parquet      their ip/metadata/md5, ORDER BY report_id
//	recruit/recruit_YYYY-MM.parquet                recruit reports with their ip/metadata/md5, ORDER BY created_at
//	rollup/rollup_YYYY-MM.parquet                  non-recruit reliable reports counted per UTC hour
//
// Every file is Parquet V2, zstd, 122,880 rows per row group, with the month and sort order in its key-value
// metadata. Paths in the manifest are relative to the directory, so a manifest can index files it does not sit
// next to (see BuildManifest).
package coldstore

import (
	"fmt"
	"path"
	"time"
)

// Prefix is the key prefix of the cold storage in the bucket; the layout below it is versioned.
const Prefix = "coldstore/v1"

const (
	// ManifestFile is the manifest's path in a cold storage directory.
	ManifestFile = "manifest.json"
	// DropPatternElementsFile is the path of the drop pattern elements dimension in a cold storage directory.
	DropPatternElementsFile = "dims/drop_pattern_elements.parquet"
)

// PreMonth is the month key of the reports created before Epoch: a few anomalous rows, most of them dated
// 1970-01-01, which are archived together in one set of files.
const PreMonth = "pre"

// Epoch is the start of the first regular month of the cold storage (penguin-stats' reports start in 2019-05).
var Epoch = time.Date(2019, time.May, 1, 0, 0, 0, 0, time.UTC)

// Kind is one of the per-month files.
type Kind string

const (
	KindDropReports Kind = "drop_reports"
	KindExtras      Kind = "drop_report_extras"
	KindRecruit     Kind = "recruit"
	KindRollup      Kind = "rollup"
)

// Kinds lists the per-month files in the order they are written and listed in the manifest.
var Kinds = []Kind{KindDropReports, KindExtras, KindRecruit, KindRollup}

var filePrefixes = map[Kind]string{
	KindDropReports: "drop_reports_",
	KindExtras:      "extras_",
	KindRecruit:     "recruit_",
	KindRollup:      "rollup_",
}

// Month is the unit of archiving: the reports with Start <= created_at < End, in UTC. The pre month has no Start.
type Month struct {
	Key   string
	Start time.Time
	End   time.Time
}

// ParseMonth parses a month key: "YYYY-MM" from 2019-05 on, or "pre".
func ParseMonth(key string) (Month, error) {
	if key == PreMonth {
		return Month{Key: PreMonth, End: Epoch}, nil
	}
	start, err := time.Parse("2006-01", key)
	if err != nil || start.Format("2006-01") != key {
		return Month{}, fmt.Errorf("coldstore: invalid month %q, want YYYY-MM or %q", key, PreMonth)
	}
	if start.Before(Epoch) {
		return Month{}, fmt.Errorf("coldstore: month %s is before %s; its reports are in %q", key, Epoch.Format("2006-01"), PreMonth)
	}
	return Month{Key: key, Start: start, End: start.AddDate(0, 1, 0)}, nil
}

// MonthOf returns the month the reports created at t are archived in.
func MonthOf(t time.Time) Month {
	t = t.UTC()
	if t.Before(Epoch) {
		return Month{Key: PreMonth, End: Epoch}
	}
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return Month{Key: start.Format("2006-01"), Start: start, End: start.AddDate(0, 1, 0)}
}

// IsPre tells whether m is the pre month.
func (m Month) IsPre() bool {
	return m.Key == PreMonth
}

// DefaultGrace is how long after its end a month is taken as complete (see Month.Over).
const DefaultGrace = 72 * time.Hour

// Over tells whether the month ended at least grace before now. A report's created_at is the time its task was
// accepted, not the time it was stored (see reportwkr), so a month still gains reports after its end while the
// report queue drains: export a month only after a grace period.
func (m Month) Over(now time.Time, grace time.Duration) bool {
	return !now.Before(m.End.Add(grace))
}

// String returns the month key.
func (m Month) String() string {
	return m.Key
}

// FilePath returns the path of a per-month file, relative to the cold storage directory.
func FilePath(kind Kind, month string) string {
	return path.Join(string(kind), filePrefixes[kind]+month+".parquet")
}

// monthOfFile returns the month key of a file name of the given kind, or false if it is not one.
func monthOfFile(kind Kind, name string) (string, bool) {
	prefix := filePrefixes[kind]
	const suffix = ".parquet"
	if len(name) <= len(prefix)+len(suffix) || name[:len(prefix)] != prefix || name[len(name)-len(suffix):] != suffix {
		return "", false
	}
	key := name[len(prefix) : len(name)-len(suffix)]
	if _, err := ParseMonth(key); err != nil {
		return "", false
	}
	return key, true
}
