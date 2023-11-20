package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
)

type DropReportArchive struct {
	DropReportService      *DropReport
	DropReportExtraService *DropReportExtra
	Config                 *appconfig.Config

	Uploader *s3manager.Uploader
	S3Client *s3.S3
}

func NewDropReportArchive(dropReportService *DropReport, dropReportExtraService *DropReportExtra, config *appconfig.Config) *DropReportArchive {
	awsConfig := aws.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(config.AWSAccessKey, config.AWSSecretKey, "")).
		WithRegion(config.DropReportArchiveS3Region)
	sess := session.Must(session.NewSession(awsConfig))
	uploader := s3manager.NewUploader(sess)
	s3client := s3.New(sess)

	return &DropReportArchive{
		DropReportService:      dropReportService,
		DropReportExtraService: dropReportExtraService,
		Config:                 config,
		Uploader:               uploader,
		S3Client:               s3client,
	}
}

func (s DropReportArchive) RunArchiveJob(ctx context.Context) error {
	targetDay := time.Now().AddDate(0, 0, -1*s.Config.NotArchiveDays)
	localT := getLocalTimeForArchive(targetDay)

	fileNameForReports := getFileNameForDropReportArhive(localT)
	fileNameForExtras := getFileNameForDropReportArhiveExtra(localT)

	reportExists, err := s.fileExistsInS3(ctx, s.Config.DropReportArchiveS3Bucket, fileNameForReports)
	if err != nil {
		return errors.Wrap(err, "failed to check if file exists in s3")
	}
	extraExists, err := s.fileExistsInS3(ctx, s.Config.DropReportArchiveS3Bucket, fileNameForExtras)
	if err != nil {
		return errors.Wrap(err, "failed to check if file exists in s3")
	}
	log.Info().Bool("report_exists", reportExists).Bool("extra_exists", extraExists).Str("target_day_CN", localT.Format("2006-01-02")).Msg("checking if archive files exist in s3")

	if reportExists && extraExists {
		log.Info().Msg("archive files already exist in s3")
		return nil
	}

	if err := s.Archive(ctx, &targetDay); err != nil {
		return errors.Wrap(err, "failed to Archive")
	}

	return nil
}

func (s DropReportArchive) Archive(ctx context.Context, date *time.Time) error {
	filePrefix := os.TempDir() + "/penguin_drop_report_archive"
	if _, err := os.Stat(filePrefix + "/drop_reports"); os.IsNotExist(err) {
		err = os.Mkdir(filePrefix+"/drop_reports", 0755)
		if err != nil {
			return errors.Wrap(err, "failed to create directory "+filePrefix+"/drop_reports")
		}
	}
	if _, err := os.Stat(filePrefix + "/drop_report_extras"); os.IsNotExist(err) {
		err = os.Mkdir(filePrefix+"/drop_report_extras", 0755)
		if err != nil {
			return errors.Wrap(err, "failed to create directory "+filePrefix+"/drop_report_extras")
		}
	}

	fileNameForReports := getFileNameForDropReportArhive(*date)
	localFilePathForReports := filePrefix + "/" + fileNameForReports
	firstId, lastId, err := s.writeDropReportArchiveFileAndUpload(ctx, date, localFilePathForReports, fileNameForReports)
	if err != nil {
		return errors.Wrap(err, "failed to writeFileAndUpload for drop reports")
	}
	log.Info().Int("first_id", firstId).Int("last_id", lastId).Msg("first and last id for drop reports")

	fileNameForExtras := getFileNameForDropReportArhiveExtra(*date)
	localFilePathForExtras := filePrefix + "/" + fileNameForExtras
	if err := s.writeDropReportExtraArchiveFileAndUpload(ctx, firstId, lastId, localFilePathForExtras, fileNameForExtras); err != nil {
		return errors.Wrap(err, "failed to writeArchiveFile for drop report extras")
	}

	// TODO: delete drop reports and extras from database

	return nil
}

func getFileNameForDropReportArhive(date time.Time) string {
	localT := getLocalTimeForArchive(date)
	return "drop_reports/drop_reports_" + localT.Format("2006-01-02") + ".jsonl.gz"
}

func getFileNameForDropReportArhiveExtra(date time.Time) string {
	localT := getLocalTimeForArchive(date)
	return "drop_report_extras/drop_report_extras_" + localT.Format("2006-01-02") + ".jsonl.gz"
}

func getLocalTimeForArchive(time time.Time) time.Time {
	loc := constant.LocMap["CN"] // we use CN server's day start time as the day start time for all servers for archive
	return time.In(loc)
}

func (s DropReportArchive) writeDropReportArchiveFileAndUpload(ctx context.Context, date *time.Time, localFilePath string, fileName string) (int, int, error) {
	firstId, lastId, err := s.writeDropReportArchiveFile(ctx, date, localFilePath)
	if err != nil {
		return firstId, lastId, err
	}

	log.Info().Str("localFilePath", localFilePath).Str("fileName", fileName).Msg("uploading file to s3")
	if err := s.uploadFileToS3(ctx, s.Config.DropReportArchiveS3Bucket, localFilePath, fileName); err != nil {
		return firstId, lastId, err
	}

	if err := os.Remove(localFilePath); err != nil {
		return firstId, lastId, errors.Wrap(err, "failed to remove file "+localFilePath)
	}

	return firstId, lastId, nil
}

func (s DropReportArchive) writeDropReportExtraArchiveFileAndUpload(ctx context.Context, idInclusiveStart int, idInclusiveEnd int, localFilePath string, fileName string) error {
	if err := s.writeDropReportExtraArchiveFile(ctx, idInclusiveStart, idInclusiveEnd, localFilePath); err != nil {
		return err
	}

	log.Info().Str("localFilePath", localFilePath).Str("fileName", fileName).Msg("uploading file to s3")
	if err := s.uploadFileToS3(ctx, s.Config.DropReportArchiveS3Bucket, localFilePath, fileName); err != nil {
		return err
	}

	if err := os.Remove(localFilePath); err != nil {
		return errors.Wrap(err, "failed to remove file "+localFilePath)
	}

	return nil
}

func (s DropReportArchive) writeDropReportArchiveFile(ctx context.Context, date *time.Time, localFilePath string) (int, int, error) {
	// If the file exists, delete it
	if _, err := os.Stat(localFilePath); err == nil {
		err = os.Remove(localFilePath)
		if err != nil {
			return 0, 0, errors.Wrap(err, "failed to remove existing file "+localFilePath)
		}
	}

	file, err := os.Create(localFilePath)
	if err != nil {
		return 0, 0, errors.Wrap(err, "failed to create file "+localFilePath)
	}

	// Create a new gzip writer
	gw := gzip.NewWriter(file)

	var dropReports []*model.DropReport
	var cursor model.Cursor
	page := 0
	totalCount := 0
	firstId := 0
	lastId := 0
	for {
		dropReports, cursor, err = s.DropReportService.GetDropReportsForArchive(ctx, &cursor, date, 10000)
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
		log.Info().Int("page", page).Int("cursor_start", cursor.Start).Int("cursor_end", cursor.End).Int("count", len(dropReports)).Msg("got drop reports")
		cursor.Start = cursor.End
		page++
		totalCount += len(dropReports)

		for _, dropReport := range dropReports {
			jsonBytes, err := json.Marshal(dropReport)
			if err != nil {
				return 0, 0, errors.Wrap(err, "failed to Marshal dropReport")
			}
			jsonBytes = append(jsonBytes, '\n')
			_, err = gw.Write(jsonBytes)
			if err != nil {
				return 0, 0, errors.Wrap(err, "failed to Write jsonStr")
			}
		}
	}
	gw.Close()
	file.Close()
	log.Info().Int("total_count", totalCount).Msg("finished writing drop reports archive file")
	return firstId, lastId, nil
}

func (s DropReportArchive) writeDropReportExtraArchiveFile(ctx context.Context, idInclusiveStart int, idInclusiveEnd int, localFilePath string) error {
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

	var extras []*model.DropReportExtra
	var cursor model.Cursor
	page := 0
	totalCount := 0
	for {
		extras, cursor, err = s.DropReportExtraService.GetDropReportExtraForArchive(ctx, &cursor, idInclusiveStart, idInclusiveEnd, 10000)
		if err != nil {
			return errors.Wrap(err, "failed to extract drop report extras")
		}
		if len(extras) == 0 {
			break
		}
		log.Info().Int("page", page).Int("cursor_start", cursor.Start).Int("cursor_end", cursor.End).Int("count", len(extras)).Msg("got extras")
		cursor.Start = cursor.End
		page++
		totalCount += len(extras)

		for _, extra := range extras {
			jsonBytes, err := json.Marshal(extra)
			if err != nil {
				return errors.Wrap(err, "failed to Marshal drop report extra")
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
	log.Info().Int("total_count", totalCount).Msg("finished writing drop report extras archive file")
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

func (s DropReportArchive) fileExistsInS3(ctx context.Context, bucket string, remoteFileKey string) (bool, error) {
	_, err := s.S3Client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("v1/" + remoteFileKey),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "NotFound":
				return false, nil
			default:
				return false, err
			}
		}
	}
	return true, nil
}
