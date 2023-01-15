package script_migrate_drop_report_extras_cols

import (
	"context"
	"net/http"
	_ "net/http/pprof"

	"github.com/felixge/fgprof"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

func run(deps CommandDeps) error {
	http.DefaultServeMux.Handle("/debug/fgprof", fgprof.Handler())
	go func() {
		log.Print(http.ListenAndServe("127.0.0.1:6060", nil))
	}()

	log.Info().Interface("deps", deps).Msg("running script")

	var err error
	if err = stage1_addSourceNameAndVersionToDropReportTable(deps); err != nil {
		return errors.Wrap(err, "failed to run stage1_addSourceNameAndVersionToDropReportTable")
	}

	log.Info().Msg("stage1_addSourceNameAndVersionToDropReportTable completed")

	if err = stage2_batchMigrateRecords(deps); err != nil {
		return errors.Wrap(err, "failed to run stage2_batchMigrateRecords")
	}

	log.Info().Msg("stage2_batchMigrateRecords completed")

	log.Info().Msg("script finished")

	return nil
}

func stage1_addSourceNameAndVersionToDropReportTable(deps CommandDeps) error {
	db := deps.DB

	_, err := db.Exec(`ALTER TABLE drop_reports ADD COLUMN source_name TEXT NULL`)
	if err != nil {
		return errors.Wrap(err, "failed to add source_name column to drop_reports table")
	}

	log.Info().Msg("source_name column added to drop_reports table")

	_, err = db.Exec(`ALTER TABLE drop_reports ADD COLUMN version TEXT NULL`)
	if err != nil {
		return errors.Wrap(err, "failed to add version column to drop_reports table")
	}

	log.Info().Msg("version column added to drop_reports table")

	_, err = db.Exec(`CREATE INDEX drop_reports_source_name_idx ON drop_reports (source_name)`)
	if err != nil {
		return errors.Wrap(err, "failed to create index on source_name column of drop_reports table")
	}

	log.Info().Msg("index created on source_name column of drop_reports table")

	_, err = db.Exec(`CREATE INDEX drop_reports_version_idx ON drop_reports (version)`)
	if err != nil {
		return errors.Wrap(err, "failed to create index on version column of drop_reports table")
	}

	log.Info().Msg("index created on version column of drop_reports table")

	return nil
}

type dropReportUpdate struct {
	ReportID int    `bun:"report_id"`
	Source   string `bun:"source_name,nullzero"`
	Version  string `bun:"version,nullzero"`
}

type dropReportExtraPartial struct {
	bun.BaseModel `bun:"drop_report_extras"`

	ReportID int    `bun:",pk,autoincrement" json:"id"`
	Source   string `json:"source" bun:"source_name"`
	Version  string `json:"version"`
}

func stage2_batchMigrateRecords(deps CommandDeps) error {
	db := deps.DB

	batchSize := 5000

	pendingDropReportUpdates := make([]*dropReportUpdate, 0, batchSize)
	dropReportExtras := make([]*dropReportExtraPartial, 0, batchSize)
	offset := 0

	ctx := context.Background()

	for {
		dropReportExtras = dropReportExtras[:0]
		pendingDropReportUpdates = pendingDropReportUpdates[:0]

		log.Info().Msgf("running stage2_batchMigrateRecords: offset=%d. selecting...", offset)

		err := db.NewSelect().
			Model(&dropReportExtras).
			ColumnExpr("report_id, source_name, version").
			OrderExpr("report_id ASC").
			Where("report_id > ?", offset).
			Limit(batchSize).
			Scan(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to select drop_report_extras records")
		}

		log.Info().
			Int("dropReportExtras", len(dropReportExtras)).
			Msg("got drop_report_extras records")

		if len(dropReportExtras) == 0 {
			log.Info().Msg("no more records to migrate")
			break
		}

		for _, dropReportExtra := range dropReportExtras {
			pendingDropReportUpdates = append(pendingDropReportUpdates, &dropReportUpdate{
				ReportID: dropReportExtra.ReportID,
				Source:   dropReportExtra.Source,
				Version:  dropReportExtra.Version,
			})
		}

		log.Info().
			Int("pendingDropReportUpdates", len(pendingDropReportUpdates)).
			Msg("got pending drop_report_updates records")

		_, err = db.NewUpdate().
			With("_data", db.NewValues(&pendingDropReportUpdates)).
			Model((*model.DropReport)(nil)).
			TableExpr("_data").
			Set("source_name = _data.source_name").
			Set("version = _data.version").
			Where("dr.report_id = _data.report_id").
			Column().
			OmitZero().
			Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to update drop_reports records")
		}

		offset = pendingDropReportUpdates[len(pendingDropReportUpdates)-1].ReportID + 1

		log.Info().Int("offsetId", offset).Msg("migrated records")
	}

	return nil
}
