package coldstore

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/pkg/errors"
)

// DimsTarget is the publication target of the dimension files (see Publish).
const DimsTarget = "dims"

// MonthTarget returns the publication target of a month's files (see Publish).
func MonthTarget(key string) string {
	return "month-" + key
}

// LocksDir is the directory, in a cold storage directory, of the lock files of Publish.
const LocksDir = ".locks"

// testHookLockWait, if set, is called when Publish finds its target locked, before it waits for the lock.
var testHookLockWait func(target string)

// Publish replaces the files of one target (MonthTarget or DimsTarget) of the cold storage directory dir and
// records them in dir's manifest, holding an exclusive lock on the target from beginning to end. write is called
// with the manifest as it is once the lock is held (an empty one if there is none), for its checks (e.g. whether the
// month is already archived); it writes the files and returns how to record them in the manifest, or nil to record
// nothing. The record is applied with UpdateManifest before the lock is released.
//
// UpdateManifest's lock only keeps concurrent writers from losing each other's entries; this one keeps the manifest
// in agreement with the files: without it, two exports of the same target could replace the files in one order and
// record them in the other, leaving entries that describe the other export's files. Distinct targets (e.g. two
// months) are published concurrently.
func Publish(dir, target string, write func(m *Manifest) (record func(*Manifest), err error)) error {
	unlock, err := lockTarget(dir, target)
	if err != nil {
		return err
	}
	defer unlock()

	name := filepath.Join(dir, ManifestFile)
	m, err := ReadManifest(name)
	if errors.Is(err, os.ErrNotExist) {
		m, err = NewManifest(), nil
	}
	if err != nil {
		return err
	}
	record, err := write(m)
	if err != nil || record == nil {
		return err
	}
	return UpdateManifest(name, func(m *Manifest) error {
		record(m)
		return nil
	})
}

// LockTargets takes the locks of Publish on the targets of dir, in the order given, and returns their release. A
// caller locking several targets must give them in a fixed order (DimsTarget, then the months in order), so that two
// such callers cannot deadlock; Publish holds one target at a time.
func LockTargets(dir string, targets []string) (func(), error) {
	var unlocks []func()
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	for _, target := range targets {
		unlock, err := lockTarget(dir, target)
		if err != nil {
			release()
			return nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	return release, nil
}

// lockTarget takes an exclusive lock on a target of the cold storage directory dir, and returns its release.
func lockTarget(dir, target string) (func(), error) {
	locks := filepath.Join(dir, LocksDir)
	if err := os.MkdirAll(locks, 0o755); err != nil {
		return nil, errors.Wrap(err, "coldstore: create lock directory")
	}
	f, err := os.OpenFile(filepath.Join(locks, target+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: open lock")
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		if testHookLockWait != nil {
			testHookLockWait(target)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, errors.Wrapf(err, "coldstore: lock %s", target)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
