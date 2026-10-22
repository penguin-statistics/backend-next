package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

func TestColdstore(t *testing.T) {
	// disabled: nothing is opened, whatever the directory
	r, err := Coldstore(fxtest.NewLifecycle(t), &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{ColdstoreDirectory: "/nonexistent"}})
	if r != nil || err != nil {
		t.Fatalf("disabled: %v, %v", r, err)
	}

	if _, err := Coldstore(fxtest.NewLifecycle(t), &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{ColdstoreEnabled: true}}); err == nil {
		t.Error("enabled without a directory: no error")
	}

	// a manifest whose file is not there fails the startup, naming the command that restores it
	dir := t.TempDir()
	m := coldstore.NewManifest()
	m.PutDim(coldstore.FileEntry{Path: coldstore.DropPatternElementsFile, Bytes: 10})
	if err := coldstore.WriteManifest(filepath.Join(dir, coldstore.ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		ColdstoreEnabled:       true,
		ColdstoreDirectory:     dir,
		ColdstoreThreads:       1,
		ColdstoreMemoryLimit:   "128MB",
		ColdstoreTempDirectory: t.TempDir(),
	}}
	_, err = Coldstore(fxtest.NewLifecycle(t), conf)
	if err == nil || !strings.Contains(err.Error(), "coldstore-pull --dir "+dir+" --dims") {
		t.Fatalf("incomplete directory: err = %v", err)
	}

	// an empty archive opens, and closes with the app
	if err := coldstore.WriteManifest(filepath.Join(dir, coldstore.ManifestFile), coldstore.NewManifest()); err != nil {
		t.Fatal(err)
	}
	lc := fxtest.NewLifecycle(t)
	r, err = Coldstore(lc, conf)
	if err != nil || r == nil {
		t.Fatalf("enabled: %v, %v", r, err)
	}
	lc.RequireStart()
	if !r.View().HotBoundary().IsZero() {
		t.Errorf("hot boundary of an empty archive = %v", r.View().HotBoundary())
	}
	lc.RequireStop()
	if entries, _ := os.ReadDir(conf.ColdstoreTempDirectory); len(entries) != 0 {
		t.Errorf("temp directory after the stop: %d entries", len(entries))
	}
}
