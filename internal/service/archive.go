package service

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-redsync/redsync/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/archiver"
)

const (
	RealmDropReports      = "drop_reports"
	RealmDropReportExtras = "drop_report_extras"

	ArchiveS3Prefix = "v1/"
)

type Archive struct {
	DropReportService      *DropReport
	DropReportExtraService *DropReportExtra
	Config                 *appconfig.Config

	s3Client *s3.Client
	lock     *redsync.Mutex
	db       *bun.DB

	dropReportsArchiver      *archiver.Archiver
	dropReportExtrasArchiver *archiver.Archiver
}

func NewArchive(dropReportService *DropReport, dropReportExtraService *DropReportExtra, conf *appconfig.Config, lock *redsync.Redsync, db *bun.DB) (*Archive, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(conf.DropReportArchiveS3Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(conf.AWSAccessKey, conf.AWSSecretKey, "")),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load aws config")
	}
	s3Client := s3.NewFromConfig(cfg)

	return &Archive{
		DropReportService:      dropReportService,
		DropReportExtraService: dropReportExtraService,
		Config:                 conf,
		s3Client:               s3Client,
		lock:                   lock.NewMutex("mutex:archiver", redsync.WithExpiry(30*time.Minute), redsync.WithTries(2)),
		db:                     db,
		dropReportsArchiver: &archiver.Archiver{
			S3Client:  s3Client,
			S3Bucket:  conf.DropReportArchiveS3Bucket,
			S3Prefix:  ArchiveS3Prefix,
			RealmName: RealmDropReports,
		},
		dropReportExtrasArchiver: &archiver.Archiver{
			S3Client:  s3Client,
			S3Bucket:  conf.DropReportArchiveS3Bucket,
			S3Prefix:  ArchiveS3Prefix,
			RealmName: RealmDropReportExtras,
		},
	}, nil
}

func (s *Archive) ArchiveByGlobalConfig(ctx context.Context) error {
	targetDay := time.Now().AddDate(0, 0, -1*s.Config.NoArchiveDays)
	return s.ArchiveByDate(ctx, targetDay, s.Config.DeleteDropReportAfterArchive)
}

func (s *Archive) ArchiveByDate(ctx context.Context, date time.Time, deleteAfterArchive bool) error {
	if err := s.lock.Lock(); err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}
	defer s.lock.Unlock()

	eg := errgroup.Group{}

	if err := s.dropReportsArchiver.Prepare(ctx, date); err != nil {
		if errors.Is(err, archiver.ErrFileAlreadyExists) {
			log.Info().
				Str("evt.name", "archive.drop_reports").
				Str("realm", RealmDropReports).
				Msg("already archived")

			return nil
		}
		return errors.Wrap(err, "failed to prepare drop reports archiver")
	}
	if err := s.dropReportExtrasArchiver.Prepare(ctx, date); err != nil {
		if errors.Is(err, archiver.ErrFileAlreadyExists) {
			log.Info().
				Str("evt.name", "archive.drop_report_extras").
				Str("realm", RealmDropReportExtras).
				Msg("already archived")

			return nil
		}
		return errors.Wrap(err, "failed to prepare drop report extras archiver")
	}

	eg.Go(func() error {
		return s.dropReportsArchiver.Collect(ctx)
	})
	eg.Go(func() error {
		return s.dropReportExtrasArchiver.Collect(ctx)
	})

	firstId, lastId, err := s.populateDropReportsToArchiver(ctx, date)
	if err != nil {
		return errors.Wrap(err, "failed to archive drop reports")
	}

	if err := s.populateDropReportExtrasToArchiver(ctx, firstId, lastId); err != nil {
		return errors.Wrap(err, "failed to archive drop report extras")
	}

	err = eg.Wait()
	log.Info().
		Str("evt.name", "archive.finished").
		Err(err).
		Msg("finished archiving")

	if deleteAfterArchive {
		log.Info().
			Str("evt.name", "archive.delete").
			Msg("deleting drop reports and extras")

		err = s.DeleteReportsAndExtras(ctx, date, firstId, lastId)
		if err != nil {
			return errors.Wrap(err, "failed to delete drop reports and extras")
		}

		log.Info().
			Str("evt.name", "archive.delete").
			Msg("finished deleting drop reports and extras")
	}

	return err
}

func (s *Archive) populateDropReportsToArchiver(ctx context.Context, date time.Time) (int, int, error) {
	ch := s.dropReportsArchiver.WriterCh()

	var dropReports []*model.DropReport
	var cursor model.Cursor
	var err error
	var page, totalCount, firstId, lastId int
	for {
		dropReports, cursor, err = s.DropReportService.GetDropReportsForArchive(ctx, &cursor, date, s.Config.DropReportArchiveBatchSize)
		if err != nil {
			return 0, 0, errors.Wrap(err, "failed to extract drop reports")
		}
		if firstId == 0 {
			firstId = cursor.Start
		}
		if cursor.End != 0 {
			lastId = cursor.End
		}
		if len(dropReports) == 0 {
			break
		}
		log.Info().
			Str("evt.name", "archive.populate.drop_reports").
			Int("page", page).
			Int("cursor_start", cursor.Start).
			Int("cursor_end", cursor.End).
			Int("count", len(dropReports)).
			Msg("got drop reports")

		cursor.Start = cursor.End
		page++
		totalCount += len(dropReports)

		for _, dropReport := range dropReports {
			ch <- dropReport
		}
	}
	close(ch)

	log.Info().Int("total_count", totalCount).Msg("finished populating drop reports")
	return firstId, lastId, nil
}

func (s *Archive) populateDropReportExtrasToArchiver(ctx context.Context, idInclusiveStart int, idInclusiveEnd int) error {
	ch := s.dropReportExtrasArchiver.WriterCh()
	var extras []*model.DropReportExtra
	var cursor model.Cursor
	var err error
	var page, totalCount int
	for {
		extras, cursor, err = s.DropReportExtraService.GetDropReportExtraForArchive(ctx, &cursor, idInclusiveStart, idInclusiveEnd, s.Config.DropReportArchiveBatchSize)
		if err != nil {
			return errors.Wrap(err, "failed to extract drop report extras")
		}
		if len(extras) == 0 {
			break
		}
		log.Info().
			Str("evt.name", "archive.populate.drop_report_extras").
			Int("page", page).
			Int("cursor_start", cursor.Start).
			Int("cursor_end", cursor.End).
			Int("count", len(extras)).
			Msg("got drop report extras")

		cursor.Start = cursor.End
		page++
		totalCount += len(extras)

		for _, extra := range extras {
			ch <- extra
		}
	}
	close(ch)

	log.Info().
		Int("total_count", totalCount).
		Msg("finished populating drop report extras")
	return nil
}

func (s *Archive) DeleteReportsAndExtras(ctx context.Context, date time.Time, idInclusiveStart int, idInclusiveEnd int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to start transaction")
	}

	log.Info().
		Str("evt.name", "archive.deletion").
		Str("date", date.Format("2006-01-02")).
		Int("first_id", idInclusiveStart).
		Int("last_id", idInclusiveEnd).
		Msg("start deleting drop reports and extras")

	var rowsAffected int64
	rowsAffected, err = s.DropReportService.DeleteDropReportsForArchive(ctx, tx, date)
	if err != nil {
		return errors.Wrap(err, "failed to delete drop reports")
	}

	log.Info().
		Int64("rows_affected", rowsAffected).
		Str("evt.name", "archive.deletion.drop_report").
		Str("date", date.Format("2006-01-02")).
		Msg("finished deleting drop reports")

	rowsAffected, err = s.DropReportExtraService.DeleteDropReportExtrasForArchive(ctx, tx, idInclusiveStart, idInclusiveEnd)
	if err != nil {
		return errors.Wrap(err, "failed to delete drop report extras")
	}

	log.Info().
		Str("evt.name", "archive.deletion.drop_report_extra").
		Int("first_id", idInclusiveStart).
		Int("last_id", idInclusiveEnd).
		Int64("rows_affected", rowsAffected).
		Msg("finished deleting drop report extras")

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit transaction")
	}

	log.Info().
		Str("evt.name", "archive.deletion.success").
		Str("date", date.Format("2006-01-02")).
		Int("first_id", idInclusiveStart).
		Int("last_id", idInclusiveEnd).
		Msg("finished committing the transaction of deleting drop reports and extras")

	return nil
}
