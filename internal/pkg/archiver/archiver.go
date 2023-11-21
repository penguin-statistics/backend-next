package archiver

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	FileExt                = ".jsonl.gz"
	LocalTempDirPattern    = "penguin_stats-archiver-*"
	ArchiverChanBufferSize = 2
)

var ErrFileAlreadyExists = errors.New("file already exists")

type Archiver struct {
	S3Client *s3.Client
	S3Bucket string

	// S3Prefix is for the files in the bucket with no leading slash but optionally (typically) with trailing slash
	// e.g. "v1/" or simply "" (empty string)
	S3Prefix string

	RealmName string

	date         time.Time
	localTempDir string
	writerCh     chan interface{}
	logger       *zerolog.Logger
}

func (a *Archiver) initLogger() {
	if a.logger == nil {
		logger := log.With().
			Str("module", "archiver").
			Str("realm", a.RealmName).
			Logger()
		a.logger = &logger
	}
}

func (a *Archiver) canonicalFilePath() string {
	loc := constant.LocMap["CN"] // we use CN server's day start time as the day start time for all servers for archive
	localT := a.date.In(loc)
	return a.RealmName + "/" + a.RealmName + "_" + localT.Format("2006-01-02") + FileExt
}

func (a *Archiver) Prepare(ctx context.Context, date time.Time) error {
	a.initLogger()

	a.logger.Info().Str("date", date.Format("2006-01-02")).Msg("preparing archiver")
	a.date = date
	a.writerCh = make(chan interface{}, ArchiverChanBufferSize)

	if err := a.assertS3FileNonExistence(ctx); err != nil {
		return errors.Wrap(err, "failed to assertFileNonExistence")
	}
	a.logger.Trace().Msg("asserted S3 file non-existence")

	if err := a.createLocalTempDir(); err != nil {
		return errors.Wrap(err, "failed to createLocalTempDir")
	}
	a.logger.Trace().Str("localTempDir", a.localTempDir).Msg("created local temp dir")

	return nil
}

func (a *Archiver) assertS3FileNonExistence(ctx context.Context) error {
	key := a.S3Prefix + a.canonicalFilePath()
	input := &s3.HeadObjectInput{
		Bucket: aws.String(a.S3Bucket),
		Key:    aws.String(key),
	}
	object, err := a.S3Client.HeadObject(ctx, input)
	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) {
			if ae.ErrorCode() == "NotFound" {
				return nil
			}
		}
		return errors.Wrap(err, "failed to invoke HeadObject")
	}
	return errors.Wrap(ErrFileAlreadyExists, fmt.Sprintf("file \"%s\" already exists in s3 with LastModified \"%s\"", key, object.LastModified))
}

func (a *Archiver) createLocalTempDir() error {
	tempDir := os.TempDir()
	dir, err := os.MkdirTemp(tempDir, LocalTempDirPattern)
	if err != nil {
		return errors.Wrap(err, "failed to create temporary directory")
	}

	a.localTempDir = dir
	return nil
}

func (a *Archiver) ensureFileBaseDir(filePath string) error {
	dir := path.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create directory")
	}
	return nil
}

// Caller MUST close the channel when it's done
func (a *Archiver) WriterCh() chan interface{} {
	return a.writerCh
}

// Caller MUST use WriterCh() to get the channel
// and ensure necessary data is sent to the channel
// before calling this function. Moreover, caller should
// ensure that Collect runs only once and runs on a different
// goroutine from the one that sends data to the channel to avoid
// deadlocks.
func (a *Archiver) Collect(ctx context.Context) error {
	if err := a.archiveToLocalFile(ctx); err != nil {
		return errors.Wrap(err, "failed to archiveToLocalFile")
	}
	a.logger.Trace().Msg("archived to local file")

	if err := a.uploadToS3(ctx); err != nil {
		return errors.Wrap(err, "failed to uploadToS3")
	}
	a.logger.Trace().Msg("uploaded to S3")

	if err := a.Cleanup(); err != nil {
		return errors.Wrap(err, "failed to Cleanup")
	}
	return nil
}

func (a *Archiver) archiveToLocalFile(ctx context.Context) error {
	localTempFilePath := path.Join(a.localTempDir, a.canonicalFilePath())
	if err := a.ensureFileBaseDir(localTempFilePath); err != nil {
		return errors.Wrap(err, "failed to ensureFileBaseDir")
	}
	a.logger.Trace().Str("localTempFilePath", localTempFilePath).Msg("ensured file base dir")

	file, err := os.OpenFile(localTempFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.Wrap(err, "failed to open file")
	}
	defer file.Close()
	a.logger.Trace().Str("localTempFilePath", localTempFilePath).Msg("opened file, ready to write gzip stream")

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	jsonEncoder := json.NewEncoder(gzipWriter)

	for {
		select {
		case <-ctx.Done():
			return nil
		case item, ok := <-a.writerCh:
			if !ok {
				a.logger.Trace().Msg("writerCh closed, exiting archiveToLocalFile (closing gzipWriter and file)")
				return nil
			}
			if err := jsonEncoder.Encode(item); err != nil {
				return errors.Wrap(err, "failed to encode item")
			}
		}
	}
}

func (a *Archiver) uploadToS3(ctx context.Context) error {
	localTempFilePath := path.Join(a.localTempDir, a.canonicalFilePath())
	file, err := os.Open(localTempFilePath)
	if err != nil {
		return errors.Wrap(err, "failed to open file")
	}
	defer file.Close()

	key := a.S3Prefix + a.canonicalFilePath()
	if _, err := a.S3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(a.S3Bucket),
		Key:               aws.String(key),
		Body:              file,
		StorageClass:      types.StorageClassGlacierIr,
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	}); err != nil {
		return errors.Wrap(err, "failed to invoke PutObject")
	}
	return nil
}

func (a *Archiver) Cleanup() error {
	if err := os.RemoveAll(a.localTempDir); err != nil {
		return errors.Wrap(err, "failed to remove temporary directory")
	}
	return nil
}
