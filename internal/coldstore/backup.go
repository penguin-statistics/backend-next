package coldstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Bucket is the backup bucket of the cold storage (AWS S3). The app never reads the cold storage from it: it is
// written when a month is archived, and read only by explicit manual operations. Nothing is downloaded to check an
// upload: every object is put with its SHA-256, which S3 checks against the body it receives, and is then compared
// with a HEAD of the object (see uploadFile).
type Bucket interface {
	// Head returns what the bucket stores under key, or nil if there is no such object.
	Head(ctx context.Context, key string) (*ObjectInfo, error)
	// Put stores body (size bytes, whose SHA-256 is sha256Hex) under key with the given storage class (empty is the
	// bucket's default). The bucket must reject a body that does not have that SHA-256.
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, sha256Hex, storageClass string) error
	// Copy copies the object under src to dst within the bucket, if src still has the ETag etag.
	Copy(ctx context.Context, src, dst, etag string) error
}

// ObjectInfo is what a HEAD of an object returns.
type ObjectInfo struct {
	Size int64
	// SHA256 is the object's SHA-256 checksum in hex, empty if it was stored without one.
	SHA256       string
	ETag         string
	LastModified time.Time
	// StorageClass is empty for STANDARD (S3 does not return it).
	StorageClass string
}

// BackupOptions says where and how the cold storage is backed up.
type BackupOptions struct {
	// Prefix is the key prefix of the cold storage directory in the bucket (Prefix, "coldstore/v1").
	Prefix string
	// StorageClass is the storage class of the data files (e.g. STANDARD_IA); the manifests are always STANDARD.
	StorageClass string
}

// ManifestVersionsDir is the directory, below the prefix of the bucket, where every manifest.json that the monthly
// archiving replaces is kept (see manifestVersionName).
const ManifestVersionsDir = "manifests"

// maxPutBytes is the largest object a single PUT can store. The largest file of the whole history is 1.13 GB.
const maxPutBytes = 5 << 30

// manifestStorageClass is the storage class of the manifests: they are small and replaced every month, which the
// minimum storage duration of the infrequent access classes would bill.
const manifestStorageClass = "STANDARD"

func (o BackupOptions) key(p string) string {
	return path.Join(o.Prefix, p)
}

// uploadFile uploads a file of the cold storage directory dir to the bucket, unless the bucket already has it, and
// confirms the upload: the local file is hashed first and must still be the one fe describes (so that a file changed
// or corrupted since its verification is never uploaded), it is put with its SHA-256, which S3 checks, and a HEAD of
// the object must then report that SHA-256 and size.
func uploadFile(ctx context.Context, b Bucket, o BackupOptions, dir string, fe FileEntry) error {
	key := o.key(fe.Path)
	if fe.Bytes > maxPutBytes {
		return fmt.Errorf("coldstore: %s has %d bytes, more than a single PUT can store", fe.Path, fe.Bytes)
	}
	if info, err := b.Head(ctx, key); err != nil {
		return err
	} else if info != nil && info.Size == fe.Bytes && info.SHA256 == fe.SHA256 {
		log.Debug().Str("key", key).Msg("coldstore: already in the bucket")
		return nil
	}

	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(fe.Path)))
	if err != nil {
		return errors.Wrap(err, "coldstore: open")
	}
	defer f.Close()
	got, err := hashFile(f)
	if err != nil {
		return err
	}
	if got.Bytes != fe.Bytes || got.SHA256 != fe.SHA256 {
		return &VerificationError{Subject: fe.Path, Problems: []string{fmt.Sprintf("%d bytes, sha256 %s; manifest: %d bytes, sha256 %s",
			got.Bytes, got.SHA256, fe.Bytes, fe.SHA256)}}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return errors.Wrap(err, "coldstore: seek")
	}
	started := time.Now()
	if err := b.Put(ctx, key, f, fe.Bytes, fe.SHA256, o.StorageClass); err != nil {
		return errors.Wrapf(err, "coldstore: upload %s", key)
	}
	if err := confirmObject(ctx, b, key, fe.Bytes, fe.SHA256); err != nil {
		return err
	}
	log.Info().Str("key", key).Int64("bytes", fe.Bytes).Dur("took", time.Since(started)).Msg("coldstore: uploaded")
	return nil
}

// confirmObject checks with a HEAD that the bucket stores size bytes with the given SHA-256 under key.
func confirmObject(ctx context.Context, b Bucket, key string, size int64, sha256Hex string) error {
	info, err := b.Head(ctx, key)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("coldstore: %s is not in the bucket after its upload", key)
	}
	if info.Size != size || info.SHA256 != sha256Hex {
		return &VerificationError{Subject: "object " + key, Problems: []string{fmt.Sprintf("%d bytes, sha256 %q; uploaded: %d bytes, sha256 %s",
			info.Size, info.SHA256, size, sha256Hex)}}
	}
	return nil
}

// manifestVersionName is the name under ManifestVersionsDir of a manifest.json that is replaced:
// manifest-<LastModified>-<the start of its SHA-256, or of its ETag if it has none>.json.
func manifestVersionName(prev *ObjectInfo) string {
	id := prev.SHA256
	if id == "" {
		id = strings.Trim(prev.ETag, `"`)
	}
	if len(id) > 16 {
		id = id[:16]
	}
	return "manifest-" + prev.LastModified.UTC().Format("20060102T150405Z") + "-" + id + ".json"
}

// uploadManifest replaces the bucket's manifest.json with content, after keeping the one it replaces under
// ManifestVersionsDir (copied within the bucket, see manifestVersionName), and confirms both.
func uploadManifest(ctx context.Context, b Bucket, o BackupOptions, content []byte) error {
	key := o.key(ManifestFile)
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	prev, err := b.Head(ctx, key)
	if err != nil {
		return err
	}
	if prev != nil {
		if prev.Size == int64(len(content)) && prev.SHA256 == sha {
			return nil
		}
		version := o.key(path.Join(ManifestVersionsDir, manifestVersionName(prev)))
		kept, err := b.Head(ctx, version)
		if err != nil {
			return err
		}
		if kept == nil {
			if err := b.Copy(ctx, key, version, prev.ETag); err != nil {
				return errors.Wrapf(err, "coldstore: keep the previous manifest as %s", version)
			}
			if kept, err = b.Head(ctx, version); err != nil {
				return err
			}
		}
		if kept == nil || kept.Size != prev.Size {
			return fmt.Errorf("coldstore: the previous manifest is not kept as %s", version)
		}
	}
	if err := b.Put(ctx, key, bytes.NewReader(content), int64(len(content)), sha, manifestStorageClass); err != nil {
		return errors.Wrapf(err, "coldstore: upload %s", key)
	}
	return confirmObject(ctx, b, key, int64(len(content)), sha)
}
