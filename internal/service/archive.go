package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
)

type Archive struct {
	DropReportService *DropReport
	S3Service         *S3
	Config            *appconfig.Config
}

func NewArchive(dropReportService *DropReport, s3Service *S3, config *appconfig.Config) *Archive {
	return &Archive{
		DropReportService: dropReportService,
		S3Service:         s3Service,
		Config:            config,
	}
}

func (s Archive) ArchiveDropReports(ctx context.Context, server string, date *time.Time) error {
	loc := constant.LocMap[server]
	localT := date.In(loc)
	filePrefix := os.TempDir() + "/penguin_drop_report_archive"
	if _, err := os.Stat(filePrefix); os.IsNotExist(err) {
		err = os.Mkdir(filePrefix, 0755)
		if err != nil {
			return errors.Wrap(err, "failed to create directory "+filePrefix)
		}
	}
	fileName := "drop_reports_" + server + "_" + localT.Format("2006-01-02") + ".jsonl.gz"
	localFilePath := filePrefix + "/" + fileName

	if err := s.writeDropReportArchiveFile(ctx, date, localFilePath); err != nil {
		return errors.Wrap(err, "failed to writeDropReportArchiveFile")
	}

	log.Info().Str("localFilePath", localFilePath).Str("fileName", fileName).Msg("uploading file to s3")

	if err := s.S3Service.UploadFileToS3(ctx, s.Config.DropReportArchiveS3Bucket, localFilePath, fileName); err != nil {
		return errors.Wrap(err, "failed to UploadFileToS3")
	}

	if err := os.Remove(localFilePath); err != nil {
		return errors.Wrap(err, "failed to remove file "+localFilePath)
	}

	return nil
}

func (s Archive) writeDropReportArchiveFile(ctx context.Context, date *time.Time, localFilePath string) error {
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
	for {
		reports, cursor, err = s.DropReportService.GetDropReportsForArchive(ctx, &cursor, "CN", date, 10000)
		if err != nil {
			return errors.Wrap(err, "failed to GetDropReportsForArchive")
		}
		if len(reports) == 0 {
			break
		}
		log.Info().Int("page", page).Int("cursor_start", cursor.Start).Int("cursor_end", cursor.End).Int("count", len(reports)).Msg("got reports")
		cursor.Start = cursor.End
		page++

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
	return nil
}
