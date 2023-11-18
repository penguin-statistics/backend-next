package script_archive_drop_reports

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

func run(deps CommandDeps) error {
	log.Info().Interface("deps", deps).Msg("running script")

	var err error
	if err = archiveDropReports(deps); err != nil {
		return errors.Wrap(err, "failed to run archiveDropReports")
	}

	log.Info().Msg("script finished")

	return nil
}

func archiveDropReports(deps CommandDeps) error {
	ctx := context.Background()

	start := time.UnixMilli(1664294400000)
	// start := time.UnixMilli(1558108800000)

	err := deps.DropReportArchiveService.Archive(ctx, &start)
	if err != nil {
		return errors.Wrap(err, "failed to ArchiveDropReports")
	}

	return nil
}
