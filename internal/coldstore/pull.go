package coldstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// ObjectSource reads the backup copy of the cold storage (the bucket's coldstore/v1 prefix).
type ObjectSource interface {
	// Get returns the content of the object at path, relative to the cold storage prefix (e.g. ManifestFile).
	Get(ctx context.Context, path string) (io.ReadCloser, error)
}

// DefaultPullMinFree is how much free space Pull leaves on each disk it downloads to by default.
const DefaultPullMinFree = 10 << 30

// PullOptions says what Pull restores.
type PullOptions struct {
	// Months are the months to restore.
	Months []string
	// Dims also restores the dimension files.
	Dims bool
	// All restores every file of the manifest. Without a local manifest, the bucket's is installed once all its
	// files are in place.
	All bool
	// MinFree is the free space, in bytes, that must be left on each disk once the files are downloaded.
	MinFree int64
}

// PullResult is what Pull did.
type PullResult struct {
	// Pulled lists the files downloaded, Present the files that were already in place.
	Pulled, Present []string
	// Bytes is the size of the files downloaded.
	Bytes int64
	// ManifestInstalled tells whether the bucket's manifest was installed (there was no local one).
	ManifestInstalled bool
}

// pullTarget is a set of files restored under one publication lock.
type pullTarget struct {
	target string
	month  string // empty for the dimensions
	files  []FileEntry
}

// Pull restores files of the local cold storage directory dir from the bucket: those of the requested months (and
// dimensions) that are missing or do not match the local manifest's size and SHA-256. It is the only code that
// reads the bucket, and it only runs when an operator runs coldstore-pull: the app never calls it.
//
// The local manifest says what the files must be; the bucket's is only read when there is no local one (with
// All). Every file is downloaded next to its destination, hashed while it is written, and moved into place only if
// it has the manifest's size and SHA-256, so a bad download never replaces anything. A file that is itself a
// symbolic link is replaced by the file, whatever it leads to: the reader refuses it (CheckLocalFiles). Before
// anything is downloaded, Pull refuses if a disk would be left with less than MinFree bytes free: each disk the
// files are written to, which differs from dir's for a subdirectory that is a link to another disk (see Manifest).
// Each month's files are restored under the month's publication lock, so a patch or an export of the month never
// runs alongside.
func Pull(ctx context.Context, src ObjectSource, dir string, o PullOptions) (*PullResult, error) {
	if !o.All && !o.Dims && len(o.Months) == 0 {
		return nil, errors.New("coldstore: nothing to pull: pass months, the dimensions, or all")
	}
	name := filepath.Join(dir, ManifestFile)
	m, err := ReadManifest(name)
	install := false
	switch {
	case errors.Is(err, os.ErrNotExist) && o.All:
		if m, err = fetchManifest(ctx, src); err != nil {
			return nil, err
		}
		if err := CheckCoverage(m); err != nil {
			return nil, errors.Wrap(err, "coldstore: the bucket's manifest")
		}
		install = true
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("coldstore: there is no manifest in %s: restore the whole cold storage with --all", dir)
	case err != nil:
		return nil, err
	}

	targets, err := pullTargets(m, o)
	if err != nil {
		return nil, err
	}
	res := &PullResult{}
	var need []pullTarget
	for _, t := range targets {
		if t.month != "" && PendingPatch(dir, t.month) {
			return nil, fmt.Errorf("coldstore: a patch of %s stopped partway: run coldstore-recover first", t.month)
		}
		nt := pullTarget{target: t.target, month: t.month}
		for _, fe := range t.files {
			ok, err := pulledFileIs(dir, fe)
			if err != nil {
				return nil, err
			}
			if ok {
				res.Present = append(res.Present, fe.Path)
				continue
			}
			nt.files = append(nt.files, fe)
			res.Bytes += fe.Bytes
		}
		if len(nt.files) > 0 {
			need = append(need, nt)
		}
	}

	if res.Bytes > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, errors.Wrap(err, "coldstore: create directory")
		}
		if err := checkFreeSpace(dir, need, o.MinFree); err != nil {
			return nil, err
		}
	}

	for _, t := range need {
		if err := pullFiles(ctx, src, dir, t, install); err != nil {
			return res, err
		}
		for _, fe := range t.files {
			res.Pulled = append(res.Pulled, fe.Path)
		}
	}

	if install {
		err := UpdateManifest(name, func(cur *Manifest) error {
			if len(cur.Months) > 0 || len(cur.Dims) > 0 {
				return errors.New("coldstore: a manifest appeared in the directory while pulling; not replacing it")
			}
			*cur = *m
			return nil
		})
		if err != nil {
			return res, err
		}
		res.ManifestInstalled = true
	}
	return res, nil
}

func pullTargets(m *Manifest, o PullOptions) ([]pullTarget, error) {
	var targets []pullTarget
	if o.All || o.Dims {
		if len(m.Dims) == 0 && !o.All {
			return nil, errors.New("coldstore: the manifest has no dimension files")
		}
		if len(m.Dims) > 0 {
			targets = append(targets, pullTarget{target: DimsTarget, files: m.Dims})
		}
	}
	if o.All {
		for _, e := range m.Months {
			targets = append(targets, pullTarget{target: MonthTarget(e.Month), month: e.Month, files: e.Files})
		}
		return targets, nil
	}
	seen := map[string]bool{}
	for _, key := range o.Months {
		if _, err := ParseMonth(key); err != nil {
			return nil, err
		}
		e := m.Month(key)
		if e == nil {
			return nil, fmt.Errorf("coldstore: month %s is not in the manifest", key)
		}
		if !seen[key] {
			seen[key] = true
			targets = append(targets, pullTarget{target: MonthTarget(key), month: key, files: e.Files})
		}
	}
	return targets, nil
}

// pullFiles downloads the files of a target under its lock. The local manifest is read again under the lock: a
// patch that published the target since Pull read it would otherwise be undone by the files it described before.
func pullFiles(ctx context.Context, src ObjectSource, dir string, t pullTarget, install bool) error {
	unlock, err := lockTarget(dir, t.target)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	switch {
	case install && errors.Is(err, os.ErrNotExist):
	case install && err == nil:
		return errors.New("coldstore: a manifest appeared in the directory while pulling; run coldstore-pull again")
	case err != nil:
		return err
	default:
		for _, fe := range t.files {
			if cur := m.fileEntry(fe.Path); cur == nil || *cur != fe {
				return fmt.Errorf("coldstore: %s changed in the manifest while pulling (a patch?); run coldstore-pull again", fe.Path)
			}
		}
	}
	for _, fe := range t.files {
		started := time.Now()
		if err := pullFile(ctx, src, dir, fe); err != nil {
			return err
		}
		log.Info().Str("file", fe.Path).Int64("bytes", fe.Bytes).Dur("took", time.Since(started)).Msg("coldstore: pulled file")
	}
	return nil
}

// fileEntry returns the entry of a file of the manifest, or nil.
func (m *Manifest) fileEntry(path string) *FileEntry {
	if d := m.Dim(path); d != nil {
		return d
	}
	for i := range m.Months {
		for k := range m.Months[i].Files {
			if m.Months[i].Files[k].Path == path {
				return &m.Months[i].Files[k]
			}
		}
	}
	return nil
}

// pullFile downloads one file next to its destination and renames it into place once it has the entry's size and
// SHA-256.
func pullFile(ctx context.Context, src ObjectSource, dir string, fe FileEntry) (err error) {
	final := filepath.Join(dir, filepath.FromSlash(fe.Path))
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return errors.Wrap(err, "coldstore: create directory")
	}
	suffix, err := tmpSuffix()
	if err != nil {
		return err
	}
	tmp := final + ".pull" + suffix
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	body, err := src.Get(ctx, fe.Path)
	if err != nil {
		return errors.Wrapf(err, "coldstore: get %s from the bucket", fe.Path)
	}
	defer body.Close()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return errors.Wrap(err, "coldstore: create")
	}
	h := sha256.New()
	// one byte more than expected, to notice a longer object without reading all of it
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, fe.Bytes+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errors.Wrapf(err, "coldstore: download %s", fe.Path)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); n != fe.Bytes || sum != fe.SHA256 {
		return fmt.Errorf("coldstore: the bucket's %s does not match the manifest (got %d bytes, sha256 %s; want %d bytes, sha256 %s): "+
			"was the month patched and not uploaded again?", fe.Path, n, sum, fe.Bytes, fe.SHA256)
	}
	if err := os.Rename(tmp, final); err != nil {
		return errors.Wrap(err, "coldstore: rename")
	}
	return syncDir(dir, []FileEntry{fe})
}

// pulledFileIs tells whether the file of fe is in dir as a regular file with fe's size and SHA-256. A file that is
// itself a symbolic link is not, whatever it leads to; its directory may be one.
func pulledFileIs(dir string, fe FileEntry) (bool, error) {
	name := filepath.Join(dir, filepath.FromSlash(fe.Path))
	st, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "coldstore: stat")
	}
	if !st.Mode().IsRegular() {
		return false, nil
	}
	return fileIs(name, fe)
}

// checkFreeSpace refuses the downloads of targets unless each file system they are written to keeps minFree bytes
// free once they are. A file is written in its directory in dir, whose file system is that of the directory, or of
// its closest existing parent, once links are resolved.
func checkFreeSpace(dir string, targets []pullTarget, minFree int64) error {
	type fileSystem struct {
		dir   string // the first destination directory on it, for statfs and the message
		bytes int64
	}
	var order []uint64
	byDevice := map[uint64]*fileSystem{}
	for _, t := range targets {
		for _, fe := range t.files {
			dest, err := existingDir(filepath.Dir(filepath.Join(dir, filepath.FromSlash(fe.Path))))
			if err != nil {
				return err
			}
			st, err := os.Stat(dest)
			if err != nil {
				return errors.Wrap(err, "coldstore: stat")
			}
			dev := uint64(st.Sys().(*syscall.Stat_t).Dev) //nolint:unconvert // the field type differs by platform
			fs, ok := byDevice[dev]
			if !ok {
				fs = &fileSystem{dir: dest}
				byDevice[dev] = fs
				order = append(order, dev)
			}
			fs.bytes += fe.Bytes
		}
	}
	for _, dev := range order {
		fs := byDevice[dev]
		free, err := freeBytes(fs.dir)
		if err != nil {
			return err
		}
		if free-fs.bytes < minFree {
			return fmt.Errorf("coldstore: refusing to download %d bytes into %s: %d bytes are free, which would leave %d, below the minimum of %d",
				fs.bytes, fs.dir, free, free-fs.bytes, minFree)
		}
	}
	return nil
}

// existingDir returns dir, or its closest parent that exists if it does not (Pull creates it).
func existingDir(dir string) (string, error) {
	for {
		_, err := os.Stat(dir)
		if err == nil || !errors.Is(err, os.ErrNotExist) || filepath.Dir(dir) == dir {
			return dir, errors.Wrap(err, "coldstore: stat")
		}
		dir = filepath.Dir(dir)
	}
}

// fetchManifest reads the bucket's manifest.
func fetchManifest(ctx context.Context, src ObjectSource) (*Manifest, error) {
	body, err := src.Get(ctx, ManifestFile)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: get the manifest from the bucket")
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, 64<<20))
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: read the bucket's manifest")
	}
	return parseManifest(b, "the bucket's "+ManifestFile)
}

// freeBytes returns the space available to unprivileged users on the file system of dir (once links are resolved).
func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, errors.Wrap(err, "coldstore: statfs")
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // the field types differ by platform
}
