package service

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

type S3 struct {
	Config *appconfig.Config

	Uploader *s3manager.Uploader
}

func NewS3(config *appconfig.Config) *S3 {
	// The session the S3 Uploader will use
	awsConfig := aws.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(config.AWSAccessKey, config.AWSSecretKey, "")).
		WithRegion(config.DropReportArchiveS3Region)
	sess := session.Must(session.NewSession(awsConfig))

	// Create an uploader with the session and default options
	uploader := s3manager.NewUploader(sess)

	return &S3{
		Config:   config,
		Uploader: uploader,
	}
}

func (s *S3) UploadFileToS3(ctx context.Context, bucket string, localFilePath string, remoteFileKey string) error {
	// Open the file
	f, err := os.Open(localFilePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Upload the file to S3
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
