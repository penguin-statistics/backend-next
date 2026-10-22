package script_coldstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
)

// The bucket's keys come from the environment only, never from a flag (a command line is visible to every user of
// the machine). They are the variables of scripts/coldstore/backfill.sh; without them, the AWS SDK's default
// credential chain is used.
const (
	envAccessKeyID     = "COLDSTORE_S3_ACCESS_KEY_ID"
	envSecretAccessKey = "COLDSTORE_S3_SECRET_ACCESS_KEY"
)

func pullCommand() *cli.Command {
	return &cli.Command{
		Name: "coldstore-pull",
		Description: "restore files of the local cold storage from the backup bucket: the files of the given months (or of all of them) that are " +
			"missing or do not match the local manifest's size and sha256. Each file is downloaded next to its destination and moved into place " +
			"only once its sha256 matches. Refuses if a disk it writes to would be left with less than --min-free-gib free. The app never reads the bucket: " +
			"this is the only way files come from it. The keys are read from " + envAccessKeyID + " and " + envSecretAccessKey +
			" (default: the AWS SDK's credential chain)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "dir",
				Aliases:  []string{"d"},
				Usage:    "local cold storage directory",
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:    "month",
				Aliases: []string{"m"},
				Usage:   "months to restore (comma-separated or repeated): YYYY-MM, or 'pre'",
			},
			&cli.BoolFlag{
				Name:  "dims",
				Usage: "restore the drop pattern elements",
			},
			&cli.BoolFlag{
				Name:  "all",
				Usage: "restore every file of the manifest; without a local manifest, the bucket's is installed once all its files are in place",
			},
			&cli.Int64Flag{
				Name:  "min-free-gib",
				Usage: "GiB that must be left free on each disk the files are written to (--dir, or where its linked subdirectories lead) once they are downloaded",
				Value: coldstore.DefaultPullMinFree >> 30,
			},
			&cli.StringFlag{
				Name:    "bucket",
				Usage:   "bucket of the backup copy",
				EnvVars: []string{"COLDSTORE_S3_BUCKET"},
			},
			&cli.StringFlag{
				Name:    "region",
				Usage:   "region of the bucket",
				EnvVars: []string{"COLDSTORE_S3_REGION"},
				Value:   "ap-southeast-1",
			},
			&cli.StringFlag{
				Name:    "prefix",
				Usage:   "key prefix of the cold storage in the bucket",
				EnvVars: []string{"COLDSTORE_S3_PREFIX"},
				Value:   coldstore.Prefix,
			},
			&cli.StringFlag{
				Name:    "endpoint",
				Usage:   "S3 endpoint, for an S3-compatible server (path-style requests); default: AWS's for the region",
				EnvVars: []string{"COLDSTORE_S3_ENDPOINT"},
			},
		},
		Action: runPull,
	}
}

func runPull(ctx *cli.Context) error {
	bucket := ctx.String("bucket")
	if bucket == "" {
		return errors.New("no bucket: pass --bucket or set COLDSTORE_S3_BUCKET")
	}
	src, err := newS3Source(ctx.Context, bucket, ctx.String("region"), ctx.String("prefix"), ctx.String("endpoint"))
	if err != nil {
		return err
	}
	started := time.Now()
	res, err := coldstore.Pull(ctx.Context, src, ctx.String("dir"), coldstore.PullOptions{
		Months:  ctx.StringSlice("month"),
		Dims:    ctx.Bool("dims"),
		All:     ctx.Bool("all"),
		MinFree: ctx.Int64("min-free-gib") << 30,
	})
	if res != nil {
		log.Info().Int("pulled", len(res.Pulled)).Int("present", len(res.Present)).Int64("bytes", res.Bytes).
			Bool("manifestInstalled", res.ManifestInstalled).Dur("took", time.Since(started)).Msg("coldstore: pull finished")
	}
	return err
}

// s3Source reads the cold storage objects under a prefix of a bucket.
type s3Source struct {
	client *s3.Client
	bucket string
	prefix string
}

func newS3Source(ctx context.Context, bucket, region, prefix, endpoint string) (*s3Source, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if id, secret := os.Getenv(envAccessKeyID), os.Getenv(envSecretAccessKey); id != "" || secret != "" {
		if id == "" || secret == "" {
			return nil, errors.New("set both " + envAccessKeyID + " and " + envSecretAccessKey + ", or neither")
		}
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(id, secret, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &s3Source{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *s3Source) Get(ctx context.Context, p string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path.Join(s.prefix, p)),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
