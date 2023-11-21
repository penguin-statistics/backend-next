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

	dropReportsArchiver      *archiver.Archiver
	dropReportExtrasArchiver *archiver.Archiver
}

func NewArchive(dropReportService *DropReport, dropReportExtraService *DropReportExtra, conf *appconfig.Config, lock *redsync.Redsync) (*Archive, error) {
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
	return s.ArchiveByDate(ctx, targetDay)
}

func (s *Archive) ArchiveByDate(ctx context.Context, date time.Time) error {
	if err := s.lock.Lock(); err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}
	defer s.lock.Unlock()

	eg := errgroup.Group{}

	if err := s.dropReportsArchiver.Prepare(ctx, date); err != nil {
		return errors.Wrap(err, "failed to prepare drop reports archiver")
	}
	if err := s.dropReportExtrasArchiver.Prepare(ctx, date); err != nil {
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
	log.Info().Err(err).Msg("finished archiving")

	return nil
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
