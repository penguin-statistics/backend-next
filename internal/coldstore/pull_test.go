package coldstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// dirSource serves a directory as the bucket, and records what was read.
type dirSource struct {
	dir string

	mu   sync.Mutex
	gets []string
}

func (s *dirSource) Get(_ context.Context, path string) (io.ReadCloser, error) {
	s.mu.Lock()
	s.gets = append(s.gets, path)
	s.mu.Unlock()
	return os.Open(filepath.Join(s.dir, filepath.FromSlash(path)))
}

func (s *dirSource) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string{}, s.gets...)
	sort.Strings(out)
	return out
}

func TestPullRestoresMissingAndCorruptFiles(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, readerMonths...)
	dir := copyStore(t, bucket)

	if err := os.Remove(filepath.Join(dir, FilePath(KindRecruit, "2019-06"))); err != nil {
		t.Fatal(err)
	}
	tamperFile(t, dir, KindDropReports, "2019-06", "times = times + 1")
	if _, err := OpenReader(ReaderConfig{Dir: dir, TempDir: t.TempDir()}); err == nil {
		t.Fatal("OpenReader of the damaged directory succeeded")
	}

	src := &dirSource{dir: bucket}
	res, err := Pull(context.Background(), src, dir, PullOptions{Months: []string{"2019-06"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{FilePath(KindDropReports, "2019-06"), FilePath(KindRecruit, "2019-06")}
	if got := src.got(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("read from the bucket: %v, want %v", got, want)
	}
	if len(res.Pulled) != 2 || len(res.Present) != 2 || res.ManifestInstalled {
		t.Errorf("result = %+v", res)
	}
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, fe := range m.Month("2019-06").Files {
		if ok, err := fileIs(filepath.Join(dir, fe.Path), fe); !ok || err != nil {
			t.Errorf("%s after the pull: %t, %v", fe.Path, ok, err)
		}
	}
	// the other months were not touched, and the directory serves again
	r := openTestReader(t, dir)
	if res, err := r.CheckIntegrity(context.Background()); err != nil || len(res.Bad) != 0 {
		t.Errorf("integrity after the pull: %+v, %v", res, err)
	}
	assertNoPullLeftovers(t, dir)

	// nothing to do the second time
	src = &dirSource{dir: bucket}
	if res, err := Pull(context.Background(), src, dir, PullOptions{All: true}); err != nil || len(res.Pulled) != 0 || len(src.got()) != 0 {
		t.Errorf("second pull: %+v, %v, read %v", res, err, src.got())
	}
}

func TestPullRefusesABadDownload(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, readerMonths...)
	dir := copyStore(t, bucket)
	name := filepath.Join(dir, FilePath(KindRollup, "2019-05"))
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	// the bucket holds another version of the file (patched locally and not uploaded again), then a truncated one
	tamperFile(t, bucket, KindRollup, "2019-05", "cnt = cnt + 1")
	_, err := Pull(context.Background(), &dirSource{dir: bucket}, dir, PullOptions{Months: []string{"2019-05"}})
	if err == nil || !strings.Contains(err.Error(), "does not match the manifest") {
		t.Fatalf("err = %v", err)
	}
	if err := os.Truncate(filepath.Join(bucket, FilePath(KindRollup, "2019-05")), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), &dirSource{dir: bucket}, dir, PullOptions{Months: []string{"2019-05"}}); err == nil {
		t.Fatal("a truncated download was accepted")
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Errorf("a bad download was moved into place: %v", err)
	}
	assertNoPullLeftovers(t, dir)
}

func TestPullChecksTheFreeSpaceFirst(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, readerMonths...)
	dir := copyStore(t, bucket)
	if err := os.Remove(filepath.Join(dir, FilePath(KindDropReports, "pre"))); err != nil {
		t.Fatal(err)
	}
	free, err := freeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := &dirSource{dir: bucket}
	_, err = Pull(context.Background(), src, dir, PullOptions{Months: []string{"pre"}, MinFree: free})
	if err == nil || !strings.Contains(err.Error(), "refusing to download") {
		t.Fatalf("err = %v", err)
	}
	if len(src.got()) != 0 {
		t.Errorf("read %v from the bucket before refusing", src.got())
	}
}

func TestPullChecksTheFreeSpaceOfEachDestination(t *testing.T) {
	// rollup/ is a link to a directory on another file system (a tmpfs), which has less space free than dir's
	other, err := os.MkdirTemp("/dev/shm", "coldstore-pull-test-")
	if err != nil {
		t.Skipf("no tmpfs at /dev/shm: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	d := newReaderTestData()
	bucket, m := writeReaderStore(t, d, readerMonths...)
	dir := copyStore(t, bucket)
	linked := filepath.Join(other, string(KindRollup))
	copyDir(t, filepath.Join(dir, string(KindRollup)), linked)
	if err := os.RemoveAll(filepath.Join(dir, string(KindRollup))); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linked, filepath.Join(dir, string(KindRollup))); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(linked, filepath.Base(FilePath(KindRollup, "2019-06")))); err != nil {
		t.Fatal(err)
	}
	dirStat, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherStat, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(dirStat, otherStat) || dirStat.Sys().(*syscall.Stat_t).Dev == otherStat.Sys().(*syscall.Stat_t).Dev {
		t.Skip("/dev/shm is on the file system of the test directory")
	}
	size := m.Month("2019-06").File(KindRollup).Bytes
	dirFree, err := freeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherFree, err := freeBytes(other)
	if err != nil {
		t.Fatal(err)
	}
	// enough on dir's file system, not on the one the file is written to
	minFree := otherFree + 1<<20
	if dirFree-size < minFree+1<<30 {
		t.Skipf("the test directory has %d bytes free, not much more than /dev/shm's %d", dirFree, otherFree)
	}

	src := &dirSource{dir: bucket}
	_, err = Pull(context.Background(), src, dir, PullOptions{Months: []string{"2019-06"}, MinFree: minFree})
	if err == nil || !strings.Contains(err.Error(), "refusing to download") || !strings.Contains(err.Error(), "into "+filepath.Join(dir, string(KindRollup))) {
		t.Fatalf("err = %v, want a refusal for the linked directory", err)
	}
	if len(src.got()) != 0 {
		t.Errorf("read %v from the bucket before refusing", src.got())
	}

	res, err := Pull(context.Background(), src, dir, PullOptions{Months: []string{"2019-06"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pulled) != 1 || res.Pulled[0] != FilePath(KindRollup, "2019-06") {
		t.Errorf("pulled %v", res.Pulled)
	}
	openTestReader(t, dir)
}

func TestPullReplacesALinkedFile(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, readerMonths...)
	dir := copyStore(t, bucket)
	// the file is a link to an identical copy: the reader refuses it all the same
	name := filepath.Join(dir, filepath.FromSlash(FilePath(KindRollup, "2019-06")))
	moved := filepath.Join(t.TempDir(), "moved.parquet")
	copyDir(t, name, moved)
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, name); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReader(ReaderConfig{Dir: dir, TempDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Fatalf("OpenReader of a linked file: %v", err)
	}

	src := &dirSource{dir: bucket}
	res, err := Pull(context.Background(), src, dir, PullOptions{Months: []string{"2019-06"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pulled) != 1 || res.Pulled[0] != FilePath(KindRollup, "2019-06") || len(res.Present) != len(Kinds)-1 {
		t.Errorf("result = %+v", res)
	}
	if st, err := os.Lstat(name); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("%s after the pull: %v, %v", name, st, err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the link's target: %v", err)
	}
	openTestReader(t, dir)
}

func TestPullInstallsTheBucketsManifestLast(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, readerMonths...)
	dir := t.TempDir()

	if _, err := Pull(context.Background(), &dirSource{dir: bucket}, dir, PullOptions{Months: []string{"2019-05"}}); err == nil ||
		!strings.Contains(err.Error(), "--all") {
		t.Errorf("pull of a month without a manifest: %v", err)
	}

	// a failure partway leaves no manifest, so the directory is never served half restored
	broken := copyStore(t, bucket)
	if err := os.Remove(filepath.Join(broken, FilePath(KindExtras, "2019-06"))); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), &dirSource{dir: broken}, dir, PullOptions{All: true}); err == nil {
		t.Fatal("pull from an incomplete bucket succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); !os.IsNotExist(err) {
		t.Errorf("manifest after a failed pull: %v", err)
	}

	res, err := Pull(context.Background(), &dirSource{dir: bucket}, dir, PullOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ManifestInstalled || len(res.Pulled)+len(res.Present) != 3*len(Kinds)+1 {
		t.Errorf("result = %+v", res)
	}
	r := openTestReader(t, dir)
	if res, err := r.CheckIntegrity(context.Background()); err != nil || len(res.Bad) != 0 {
		t.Errorf("integrity after the pull: %+v, %v", res, err)
	}
}

func TestPullRefusals(t *testing.T) {
	d := newReaderTestData()
	bucket, _ := writeReaderStore(t, d, "pre", "2019-05")
	dir := copyStore(t, bucket)
	if err := os.MkdirAll(filepath.Join(dir, JournalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath(dir, MonthTarget("pre")), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		o    PullOptions
		want string
	}{
		{PullOptions{}, "nothing to pull"},
		{PullOptions{Months: []string{"2019-06"}}, "month 2019-06 is not in the manifest"},
		{PullOptions{Months: []string{"2019-13"}}, "invalid month"},
		{PullOptions{Months: []string{"pre"}}, "run coldstore-recover first"},
	} {
		src := &dirSource{dir: bucket}
		_, err := Pull(context.Background(), src, dir, c.o)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.o, err, c.want)
		}
		if len(src.got()) != 0 {
			t.Errorf("%+v: read %v from the bucket", c.o, src.got())
		}
	}
}

// assertNoPullLeftovers checks that no download is left next to the files.
func assertNoPullLeftovers(t *testing.T, dir string) {
	t.Helper()
	for _, f := range listFiles(t, dir) {
		if strings.Contains(f, ".pull.") {
			t.Errorf("leftover %s", f)
		}
	}
}

func ExamplePullCommand() {
	fmt.Println(PullCommand("/data/coldstore-v1", "--month 2023-10"))
	// Output: backend run-script coldstore-pull --dir /data/coldstore-v1 --month 2023-10 (see docs/coldstore.md)
}
