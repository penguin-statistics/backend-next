package reportwkr

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

// partitionInterval is how often the worker makes sure that the report partitions of the current and the next month
// exist on the new database.
const partitionInterval = time.Hour

// checkReportSchema fails unless the database has the schema that reportSchemaV2 says it has (see
// appconfig.ConfigSpec.ReportSchemaV2Enabled): the partitioned schema of scripts/newdb, with drop_reports
// partitioned and drop_report_extras.created_at, or today's, with neither. With a mismatch every report task would
// fail to store, be retried and end up in the dead-letter stream, so the worker refuses to start instead.
func (w *Worker) checkReportSchema(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var partitioned, extrasCreatedAt bool
	err := w.DB.NewRaw(`SELECT EXISTS (SELECT FROM pg_class WHERE oid = to_regclass('drop_reports') AND relkind = 'p'),
		EXISTS (SELECT FROM pg_attribute
		WHERE attrelid = to_regclass('drop_report_extras') AND attname = 'created_at' AND NOT attisdropped)`).
		Scan(ctx, &partitioned, &extrasCreatedAt)
	if err != nil {
		return errors.Wrap(err, "failed to check the schema of the report tables")
	}
	if partitioned == w.reportSchemaV2 && extrasCreatedAt == w.reportSchemaV2 {
		return nil
	}
	return errors.Errorf("ReportSchemaV2Enabled is %t, but the database has a partitioned drop_reports: %t, drop_report_extras.created_at: %t; "+
		"set PENGUIN_V3_REPORT_SCHEMA_V2_ENABLED to match the database", w.reportSchemaV2, partitioned, extrasCreatedAt)
}

// ensureReportPartitions creates the partitions of the report tables for the month of now and the next one (UTC)
// that do not exist, with create_report_partitions of scripts/newdb/schema.sql. The new database has no default
// partition: a report of a month without partition fails to store, so the next month is created a month ahead.
// Creating a partition locks its parent table, so it gives up after a short lock_timeout instead of queueing the
// writes of reports behind a long read; one that exists is skipped without a lock.
func (w *Worker) ensureReportPartitions(ctx context.Context, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	month := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	return w.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "SELECT create_report_partitions(?::date), create_report_partitions(?::date)",
			month.Format(time.DateOnly), month.AddDate(0, 1, 0).Format(time.DateOnly))
		return errors.Wrap(err, "failed to create the report partitions")
	})
}

// maintainReportPartitions runs ensureReportPartitions now and every partitionInterval until the worker stops. A
// failure is logged and tried again at the next run.
func (w *Worker) maintainReportPartitions() {
	defer w.wg.Done()
	ticker := time.NewTicker(partitionInterval)
	defer ticker.Stop()
	for {
		if err := w.ensureReportPartitions(context.Background(), time.Now()); err != nil {
			log.Error().
				Err(err).
				Str("evt.name", "reportwkr.partitions.failed").
				Msg("failed to create the report partitions of the current and the next month; trying again later")
		}
		select {
		case <-w.stopping:
			return
		case <-ticker.C:
		}
	}
}
