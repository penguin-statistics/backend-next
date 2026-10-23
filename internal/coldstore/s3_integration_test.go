package coldstore

// The S3 client of the backup bucket against an S3-compatible server. Skipped unless PENGUIN_TEST_S3_ENDPOINT (with
// PENGUIN_TEST_S3_ACCESS_KEY and PENGUIN_TEST_S3_SECRET_KEY) points at a disposable server, e.g.
//
//	docker run -d --rm -p 127.0.0.1:59000:9000 quay.io/minio/minio server /data
//	PENGUIN_TEST_S3_ENDPOINT=http://127.0.0.1:59000 PENGUIN_TEST_S3_ACCESS_KEY=minioadmin \
//	PENGUIN_TEST_S3_SECRET_KEY=minioadmin go test -run S3 ./internal/coldstore/
//
// The test creates a bucket of its own.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func newTestS3Bucket(t *testing.T) *S3Bucket {
	t.Helper()
	endpoint := os.Getenv("PENGUIN_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PENGUIN_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	name := fmt.Sprintf("coldstore-test-%d", time.Now().UnixNano())
	b, err := NewS3Bucket(ctx, S3Options{
		Bucket: name, Region: "us-east-1", Endpoint: endpoint,
		AccessKey: os.Getenv("PENGUIN_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("PENGUIN_TEST_S3_SECRET_KEY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatal(err)
	}
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestS3Bucket(t *testing.T) {
	ctx := context.Background()
	b := newTestS3Bucket(t)

	if info, err := b.Head(ctx, "missing"); err != nil || info != nil {
		t.Fatalf("head of a missing object = %+v, %v", info, err)
	}
	data := []byte("some parquet bytes")
	if err := b.Put(ctx, "a/b.parquet", bytes.NewReader(data), int64(len(data)), sha256Hex(data), "STANDARD"); err != nil {
		t.Fatal(err)
	}
	info, err := b.Head(ctx, "a/b.parquet")
	if err != nil || info == nil || info.Size != int64(len(data)) || info.SHA256 != sha256Hex(data) || info.ETag == "" || info.LastModified.IsZero() {
		t.Fatalf("head = %+v, %v", info, err)
	}

	// a body that is not the one the checksum was computed for is rejected, and stores nothing
	other := []byte("some parquet bytez")
	if err := b.Put(ctx, "a/c.parquet", bytes.NewReader(other), int64(len(other)), sha256Hex(data), "STANDARD"); err == nil {
		t.Error("a body with another sha256 was accepted")
	}
	if info, err := b.Head(ctx, "a/c.parquet"); err != nil || info != nil {
		t.Errorf("head after a rejected put = %+v, %v", info, err)
	}

	// a copy only while the source is unchanged
	if err := b.Copy(ctx, "a/b.parquet", "a/copy.parquet", info.ETag); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Head(ctx, "a/copy.parquet"); err != nil || got == nil || got.Size != info.Size {
		t.Errorf("copy = %+v, %v", got, err)
	}
	if err := b.Copy(ctx, "a/b.parquet", "a/copy2.parquet", `"0123"`); err == nil {
		t.Error("copy of a changed source succeeded")
	}
}

// The uploads of the archiving through the S3 client: files and manifests, the previous manifest kept.
func TestS3Upload(t *testing.T) {
	ctx := context.Background()
	b := newTestS3Bucket(t)
	o := BackupOptions{Prefix: Prefix, StorageClass: "STANDARD"} // MinIO accepts STANDARD and REDUCED_REDUNDANCY only
	dir := t.TempDir()
	content := bytes.Repeat([]byte("0123456789"), 100000)
	name := filepath.Join(dir, FilePath(KindRollup, "2026-08"))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fe := FileEntry{Path: FilePath(KindRollup, "2026-08"), SHA256: sha256Hex(content), Bytes: int64(len(content))}
	if err := uploadFile(ctx, b, o, dir, fe); err != nil {
		t.Fatal(err)
	}
	if err := uploadFile(ctx, b, o, dir, fe); err != nil {
		t.Fatal(err)
	}
	// a local file that is not the manifest's is not uploaded
	if err := os.WriteFile(name, append(content, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := fe
	stale.SHA256, stale.Bytes = sha256Hex(content[:10]), 10
	if err := uploadFile(ctx, b, o, dir, stale); err == nil || !IsVerificationError(err) {
		t.Errorf("upload of a changed file = %v", err)
	}

	for i, m := range []string{`{"v":1}`, `{"v":2}`, `{"v":2}`, `{"v":3}`} {
		if err := uploadManifest(ctx, b, o, []byte(m)); err != nil {
			t.Fatalf("manifest %d: %v", i, err)
		}
	}
	info, err := b.Head(ctx, path.Join(Prefix, ManifestFile))
	if err != nil || info == nil || info.SHA256 != sha256Hex([]byte(`{"v":3}`)) {
		t.Errorf("manifest = %+v, %v", info, err)
	}
	out, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(path.Join(Prefix, ManifestVersionsDir) + "/")})
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, obj := range out.Contents {
		kept = append(kept, aws.ToString(obj.Key))
	}
	// v1 and v2 were replaced; uploading v2 again changed nothing
	if len(kept) != 2 || !strings.Contains(kept[0]+kept[1], sha256Hex([]byte(`{"v":1}`))[:16]) {
		t.Errorf("kept manifests: %v", kept)
	}
}
