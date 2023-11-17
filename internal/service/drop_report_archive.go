package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
)

type DropReportArchive struct {
	DropReportService *DropReport
	Config            *appconfig.Config

	Uploader *s3manager.Uploader
}

func NewDropReportArchive(dropReportService *DropReport, config *appconfig.Config) *DropReportArchive {
	awsConfig := aws.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(config.AWSAccessKey, config.AWSSecretKey, "")).
		WithRegion(config.DropReportArchiveS3Region)
	sess := session.Must(session.NewSession(awsConfig))
	uploader := s3manager.NewUploader(sess)

	return &DropReportArchive{
		DropReportService: dropReportService,
		Config:            config,
		Uploader:          uploader,
	}
}

func (s DropReportArchive) ArchiveDropReports(ctx context.Context, date *time.Time) error {
	loc := constant.LocMap["CN"] // we use CN server's day start time as the day start time for all servers for archive
	localT := date.In(loc)
	filePrefix := os.TempDir() + "/penguin_drop_report_archive"
	if _, err := os.Stat(filePrefix); os.IsNotExist(err) {
		err = os.Mkdir(filePrefix, 0755)
		if err != nil {
			return errors.Wrap(err, "failed to create directory "+filePrefix)
		}
	}
	fileName := "drop_reports_" + localT.Format("2006-01-02") + ".jsonl.gz"
	localFilePath := filePrefix + "/" + fileName

	if err := s.writeDropReportArchiveFile(ctx, date, localFilePath); err != nil {
		return errors.Wrap(err, "failed to writeDropReportArchiveFile")
	}

	log.Info().Str("localFilePath", localFilePath).Str("fileName", fileName).Msg("uploading file to s3")

	if err := s.uploadFileToS3(ctx, s.Config.DropReportArchiveS3Bucket, localFilePath, fileName); err != nil {
		return errors.Wrap(err, "failed to UploadFileToS3")
	}

	if err := os.Remove(localFilePath); err != nil {
		return errors.Wrap(err, "failed to remove file "+localFilePath)
	}

	return nil
}

func (s DropReportArchive) writeDropReportArchiveFile(ctx context.Context, date *time.Time, localFilePath string) error {
	// If the file exists, delete it
	if _, err := os.Stat(localFilePath); err == nil {
		err = os.Remove(localFilePath)
		if err != nil {
			return errors.Wrap(err, "failed to remove existing file "+localFilePath)
		}
	}

	file, err := os.Create(localFilePath)
	if err != nil {
		return errors.Wrap(err, "failed to create file "+localFilePath)
	}

	// Create a new gzip writer
	gw := gzip.NewWriter(file)

	var reports []*model.DropReport
	var cursor model.Cursor
	page := 0
	totalCount := 0
	for {
		reports, cursor, err = s.DropReportService.GetDropReportsForArchive(ctx, &cursor, date, 10000)
		if err != nil {
			return errors.Wrap(err, "failed to GetDropReportsForArchive")
		}
		if len(reports) == 0 {
			break
		}
		log.Info().Int("page", page).Int("cursor_start", cursor.Start).Int("cursor_end", cursor.End).Int("count", len(reports)).Msg("got reports")
		cursor.Start = cursor.End
		page++
		totalCount += len(reports)

		for _, report := range reports {
			jsonBytes, err := json.Marshal(report)
			if err != nil {
				return errors.Wrap(err, "failed to Marshal report")
			}
			jsonBytes = append(jsonBytes, '\n')
			_, err = gw.Write(jsonBytes)
			if err != nil {
				return errors.Wrap(err, "failed to Write jsonStr")
			}
		}
	}
	gw.Close()
	file.Close()
	log.Info().Int("total_count", totalCount).Msg("finished writing file")
	return nil
}

func (s DropReportArchive) uploadFileToS3(ctx context.Context, bucket string, localFilePath string, remoteFileKey string) error {
	f, err := os.Open(localFilePath)
	if err != nil {
		return err
	}
	defer f.Close()

	result, err := s.Uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("v1/" + remoteFileKey),
		Body:   f,
	})
	if err != nil {
		return err
	}

	log.Info().Str("location", result.Location).Msg("Successfully uploaded")
	return nil
}
