package script_archive_drop_reports

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

func run(deps CommandDeps, dateStr string) error {
	log.Info().Interface("deps", deps).Str("date", dateStr).Msg("running script")

	var err error

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return errors.Wrap(err, "failed to parse date")
	}

	if err = archiveDropReports(deps, date); err != nil {
		return errors.Wrap(err, "failed to run archiveDropReports")
	}

	log.Info().Msg("script finished")

	return nil
}

func archiveDropReports(deps CommandDeps, date time.Time) error {
	ctx := context.Background()

	err := deps.DropReportArchiveService.Archive(ctx, &date)
	if err != nil {
		return errors.Wrap(err, "failed to ArchiveDropReports")
	}

	return nil
}
