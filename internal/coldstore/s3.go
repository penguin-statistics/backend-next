package coldstore

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/pkg/errors"
)

// S3Options configures the backup bucket.
type S3Options struct {
	Bucket string
	Region string
	// Endpoint is the URL of an S3-compatible server (e.g. MinIO in tests), addressed with path-style URLs; empty
	// is AWS S3.
	Endpoint             string
	AccessKey, SecretKey string
}

// S3Bucket is the Bucket of an S3 bucket.
type S3Bucket struct {
	client *s3.Client
	bucket string
}

// NewS3Bucket returns the Bucket of an S3 bucket. It makes no request.
func NewS3Bucket(ctx context.Context, o S3Options) (*S3Bucket, error) {
	if o.Bucket == "" {
		return nil, errors.New("coldstore: no backup bucket")
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(o.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(o.AccessKey, o.SecretKey, "")),
	)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: load aws config")
	}
	client := s3.NewFromConfig(cfg, func(so *s3.Options) {
		if o.Endpoint != "" {
			so.BaseEndpoint = aws.String(o.Endpoint)
			so.UsePathStyle = true
		}
	})
	return &S3Bucket{client: client, bucket: o.Bucket}, nil
}

func (b *S3Bucket) Head(ctx context.Context, key string) (*ObjectInfo, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(b.bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) && (ae.ErrorCode() == "NotFound" || ae.ErrorCode() == "NoSuchKey") {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "coldstore: head %s", key)
	}
	info := &ObjectInfo{
		Size:         aws.ToInt64(out.ContentLength),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		StorageClass: string(out.StorageClass),
	}
	if c := aws.ToString(out.ChecksumSHA256); c != "" {
		// a checksum of a multipart upload is a checksum of the parts' checksums, followed by -<parts>; it never
		// matches a file's
		if raw, err := base64.StdEncoding.DecodeString(c); err == nil && !strings.Contains(c, "-") {
			info.SHA256 = hex.EncodeToString(raw)
		} else {
			info.SHA256 = c
		}
	}
	return info, nil
}

func (b *S3Bucket) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, sha256Hex, storageClass string) error {
	raw, err := hex.DecodeString(sha256Hex)
	if err != nil || len(raw) != 32 {
		return errors.Errorf("coldstore: invalid sha256 %q", sha256Hex)
	}
	// x-amz-checksum-sha256: S3 computes the body's SHA-256 and rejects the upload (BadDigest) unless it is this one
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(b.bucket),
		Key:               aws.String(key),
		Body:              body,
		ContentLength:     aws.Int64(size),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		ChecksumSHA256:    aws.String(base64.StdEncoding.EncodeToString(raw)),
		StorageClass:      types.StorageClass(storageClass),
	})
	return errors.Wrapf(err, "coldstore: put %s", key)
}

func (b *S3Bucket) Copy(ctx context.Context, src, dst, etag string) error {
	_, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(b.bucket),
		Key:               aws.String(dst),
		CopySource:        aws.String(url.PathEscape(b.bucket) + "/" + escapeKey(src)),
		CopySourceIfMatch: aws.String(etag),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		StorageClass:      types.StorageClass(manifestStorageClass),
	})
	return errors.Wrapf(err, "coldstore: copy %s to %s", src, dst)
}

// escapeKey URL-encodes each segment of an object key, as x-amz-copy-source requires.
func escapeKey(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
