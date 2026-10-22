package coldstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// IntegrityStateFile, in the Reader's TempDir, holds the time the last integrity check completed.
const IntegrityStateFile = "coldstore-integrity-checked-at"

// firstIntegrityCheckDelay is how long after the start the first integrity check runs when none is recorded.
const firstIntegrityCheckDelay = time.Hour

// IntegrityResult is what an integrity check found.
type IntegrityResult struct {
	// Files counts the files hashed.
	Files int
	// Bad lists the files that are missing, unreadable or do not match the manifest, with how to restore them.
	Bad []string
	// Skipped lists the months not checked because a patch of them stopped partway (see RecoverPatches).
	Skipped []string
}

// integrityChunk is how much of a file the integrity check hashes between two looks at its context.
const integrityChunk = 4 << 20

// testHookIntegrityChunk, if set, is called each time the integrity check has hashed a chunk of a file.
var testHookIntegrityChunk func(path string)

// CheckIntegrity hashes every file of the current view, one at a time, and compares it with the manifest's size and
// SHA-256. It only reads the local files (it never downloads anything; see Pull to restore a file) and runs on a
// thread of its own at the lowest CPU and I/O priority. Each bad file is logged and counted; the result lists them.
//
// The months (and the dimensions) are those of the current view, but each one is checked against the manifest file
// as it is now, holding the shared lock of its target that the queries take (see Reader.Query): a publication of it
// cannot run meanwhile, one that runs is waited for, and a patch committed since the view was loaded is checked
// against its own entry instead of being reported as corruption. A month whose patch stopped partway is skipped
// (its queries already fail, see Reader.Query). The check stops with ctx's error as soon as ctx ends, even within a
// file; it is then not completed: the metrics of the last completed check stay as they were.
func (r *Reader) CheckIntegrity(ctx context.Context) (IntegrityResult, error) {
	var res IntegrityResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		// the thread is never unlocked: it exits with the goroutine, and its lowered priority with it
		runtime.LockOSThread()
		lowerThreadPriority()
		res, err = r.checkIntegrity(ctx)
	}()
	<-done
	return res, err
}

func (r *Reader) checkIntegrity(ctx context.Context) (IntegrityResult, error) {
	targets := []string{DimsTarget}
	for _, e := range r.View().manifest.Months {
		targets = append(targets, MonthTarget(e.Month))
	}

	started := time.Now()
	var res IntegrityResult
	for _, target := range targets {
		if err := r.checkTarget(ctx, target, &res); err != nil {
			return res, err
		}
	}

	metricIntegrityLastMismatches.Set(float64(len(res.Bad)))
	metricIntegrityLastCompleted.SetToCurrentTime()
	ev := log.Info()
	if len(res.Bad) > 0 {
		ev = log.Error()
	}
	ev.Str("evt.name", "coldstore.integrity.done").
		Int("files", res.Files).
		Int("bad", len(res.Bad)).
		Strs("skipped", res.Skipped).
		Dur("took", time.Since(started)).
		Msg("coldstore: integrity check completed")
	return res, nil
}

// checkTarget hashes the files of a target (DimsTarget or a MonthTarget) as the manifest file lists them now, under
// the target's shared lock, and adds what it found to res. It returns an error only when the check cannot go on: ctx
// ended, or the lock or the manifest cannot be read.
func (r *Reader) checkTarget(ctx context.Context, target string, res *IntegrityResult) error {
	lock, err := os.OpenFile(filepath.Join(r.dir, LocksDir, target+".lock"), os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return errors.Wrap(err, "coldstore: open lock")
	}
	defer lock.Close()
	if err := lockShared(ctx, lock, target); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	key, isMonth := strings.CutPrefix(target, MonthTarget(""))
	if isMonth && PendingPatch(r.dir, key) {
		// under the lock, no publication runs: this one stopped partway, and its files may be of two generations
		res.Skipped = append(res.Skipped, key)
		log.Warn().
			Str("evt.name", "coldstore.integrity.skipped").
			Str("month", key).
			Msg("coldstore: integrity check skips a month whose patch stopped partway; complete or undo it with: " +
				"backend run-script coldstore-recover --dir " + r.dir + " --month " + key)
		return nil
	}
	m, err := ReadManifest(filepath.Join(r.dir, ManifestFile))
	if err != nil {
		return err
	}
	entries, restore := m.Dims, "--dims"
	if isMonth {
		e := m.Month(key)
		if e == nil {
			// no longer archived: nothing to check
			return nil
		}
		entries, restore = e.Files, "--month "+key
	}

	for _, fe := range entries {
		got, err := hashExisting(ctx, r.dir, fe.Path)
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		var problem, result string
		switch {
		case errors.Is(err, os.ErrNotExist):
			problem, result = fe.Path+" is missing", "missing"
		case err != nil:
			problem, result = fmt.Sprintf("%s cannot be read: %v", fe.Path, err), "missing"
		case got.Bytes != fe.Bytes || got.SHA256 != fe.SHA256:
			problem, result = fmt.Sprintf("%s has %d bytes, sha256 %s; the manifest says %d bytes, sha256 %s",
				fe.Path, got.Bytes, got.SHA256, fe.Bytes, fe.SHA256), "mismatch"
		default:
			result = "ok"
		}
		res.Files++
		metricIntegrityFiles.WithLabelValues(result).Inc()
		if problem == "" {
			continue
		}
		cmd := PullCommand(r.dir, restore)
		res.Bad = append(res.Bad, problem+"; restore it with: "+cmd)
		log.Error().
			Str("evt.name", "coldstore.integrity.bad_file").
			Str("file", fe.Path).
			Str("result", result).
			Str("problem", problem).
			Str("restore", cmd).
			Msg("coldstore: a file of the local cold storage does not match the manifest")
	}
	return nil
}

// hashExisting returns the size and SHA-256 of a file of the cold storage directory, as describeExisting does, but
// looks at ctx between chunks of integrityChunk bytes and stops with its error once it ends, so that a large file
// does not hold back the end of the check (Reader.Close waits for it).
func hashExisting(ctx context.Context, dir, path string) (FileEntry, error) {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		return FileEntry{}, errors.Wrap(err, "coldstore: open")
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return FileEntry{}, err
		}
		n, err := io.CopyBuffer(h, io.LimitReader(f, integrityChunk), buf)
		size += n
		if err != nil {
			return FileEntry{}, errors.Wrap(err, "coldstore: read")
		}
		if testHookIntegrityChunk != nil {
			testHookIntegrityChunk(path)
		}
		if n < integrityChunk {
			return FileEntry{Path: path, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: size}, nil
		}
	}
}

func (r *Reader) integrityLoop(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-r.stop
		cancel()
	}()
	state := filepath.Join(r.tempRoot, IntegrityStateFile)
	// notBefore holds back the next check in memory too: after a failed check, which records nothing, and after a
	// check whose time could not be recorded, so that neither starts the next one at once
	var notBefore time.Time
	for {
		due := nextIntegrityCheck(state, interval, time.Now())
		if notBefore.After(due) {
			due = notBefore
		}
		t := time.NewTimer(time.Until(due))
		select {
		case <-r.stop:
			t.Stop()
			return
		case <-t.C:
		}
		if _, err := r.CheckIntegrity(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			retry := min(firstIntegrityCheckDelay, interval)
			log.Error().Err(err).Str("evt.name", "coldstore.integrity.failed").Dur("retry_in", retry).
				Msg("coldstore: integrity check failed; it is not recorded and runs again later")
			notBefore = time.Now().Add(retry)
			continue
		}
		notBefore = time.Now().Add(interval)
		if err := os.WriteFile(state, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
			log.Warn().Err(err).Str("evt.name", "coldstore.integrity.state").Str("file", state).
				Msg("coldstore: cannot record the integrity check; this process waits the interval, a restart may check early")
		}
	}
}

// nextIntegrityCheck returns when the next integrity check is due: interval after the last one recorded in state,
// or firstIntegrityCheckDelay (at most interval) after now if none is.
func nextIntegrityCheck(state string, interval time.Duration, now time.Time) time.Time {
	if b, err := os.ReadFile(state); err == nil {
		if last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && !last.After(now) {
			return last.Add(interval)
		}
	}
	return now.Add(min(firstIntegrityCheckDelay, interval))
}
