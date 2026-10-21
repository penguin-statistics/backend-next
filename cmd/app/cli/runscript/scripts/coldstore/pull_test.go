package script_coldstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
)

// TestPullFromS3 runs coldstore-pull against a fake S3 server (path-style GETs of a directory's files).
func TestPullFromS3(t *testing.T) {
	bucketDir := t.TempDir()
	files := map[string]string{
		"dims/drop_pattern_elements.parquet": "dims",
		// not in the manifest: never read
		"drop_reports/drop_reports_pre.parquet": "reports",
	}
	m := coldstore.NewManifest()
	for p, content := range files {
		name := filepath.Join(bucketDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dims := fileEntry(t, bucketDir, "dims/drop_pattern_elements.parquet")
	m.PutDim(dims)
	if err := coldstore.WriteManifest(filepath.Join(bucketDir, coldstore.ManifestFile), m); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		key, ok := strings.CutPrefix(r.URL.Path, "/penguin-backup/coldstore/v1/")
		if r.Method != http.MethodGet || !ok {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, filepath.Join(bucketDir, filepath.FromSlash(key)))
	}))
	defer srv.Close()

	t.Setenv("COLDSTORE_S3_ACCESS_KEY_ID", "AKIDTEST")
	t.Setenv("COLDSTORE_S3_SECRET_ACCESS_KEY", "secret")
	dir := t.TempDir()
	app := &cli.App{Commands: Commands(func() CommandDeps { t.Fatal("coldstore-pull needs no configuration"); return CommandDeps{} })}
	err := app.RunContext(context.Background(), []string{"app", "coldstore-pull", "--dir", dir, "--all", "--min-free-gib", "0",
		"--bucket", "penguin-backup", "--endpoint", srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "dims", "drop_pattern_elements.parquet")); err != nil || string(b) != "dims" {
		t.Errorf("pulled dims = %q, %v", b, err)
	}
	if _, err := coldstore.ReadManifest(filepath.Join(dir, coldstore.ManifestFile)); err != nil {
		t.Errorf("manifest: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Errorf("requests = %v", requests)
	}
	for _, r := range requests {
		// signed with the keys of the environment
		if !strings.Contains(r, "Credential=AKIDTEST/") {
			t.Errorf("request %q is not signed with the configured key", r)
		}
	}

	// the keys are both required, or neither
	t.Setenv("COLDSTORE_S3_SECRET_ACCESS_KEY", "")
	err = app.RunContext(context.Background(), []string{"app", "coldstore-pull", "--dir", dir, "--all", "--bucket", "penguin-backup", "--endpoint", srv.URL})
	if err == nil || !strings.Contains(err.Error(), "or neither") {
		t.Errorf("err = %v", err)
	}
	if err := app.RunContext(context.Background(), []string{"app", "coldstore-pull", "--dir", dir, "--all"}); err == nil || !strings.Contains(err.Error(), "no bucket") {
		t.Errorf("without bucket: err = %v", err)
	}
}

func fileEntry(t *testing.T, dir, p string) coldstore.FileEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return coldstore.FileEntry{Path: p, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(b)), Rows: 1}
}
