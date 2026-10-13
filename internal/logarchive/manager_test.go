package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type recordingUploader struct {
	fail     bool
	uploads  map[string][]byte
	onUpload func()
}

func (u *recordingUploader) UploadVerified(_ context.Context, key, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if u.uploads == nil {
		u.uploads = map[string][]byte{}
	}
	u.uploads[key] = data
	if u.onUpload != nil {
		u.onUpload()
	}
	if u.fail {
		return errors.New("verification failed")
	}
	return nil
}
func testWorker(dir string, uploader verifiedUploader) *manager {
	directoryState.Lock()
	registry := directoryState.current
	directoryState.Unlock()
	return &manager{store: uploader, directories: func() []string { return []string{dir} }, registry: registry}
}
func writeFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestManagerPreservesFailedUploadAndRecoversOnRestart(t *testing.T) {
	dir := t.TempDir()
	name := "v1-responses-2026-10-13T180000-request-id.log"
	path := filepath.Join(dir, name)
	original := bytes.Repeat([]byte("request and full response\n"), 500)
	writeFixture(t, path, original)
	failed := &recordingUploader{fail: true}
	if err := testWorker(dir, failed).scan(context.Background()); err == nil {
		t.Fatal("expected verification failure")
	}
	local, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(local, original) {
		t.Fatalf("local log changed: %v", err)
	}
	successful := &recordingUploader{}
	if errScan := testWorker(dir, successful).scan(context.Background()); errScan != nil {
		t.Fatal(errScan)
	}
	if _, errStat := os.Stat(path); !os.IsNotExist(errStat) {
		t.Fatalf("verified log not reclaimed: %v", errStat)
	}
	if len(successful.uploads) != 1 {
		t.Fatalf("uploads: %d", len(successful.uploads))
	}
	for key, data := range successful.uploads {
		sum := sha256.Sum256(data)
		if key != hex.EncodeToString(sum[:])+"/"+name+".gz" {
			t.Fatalf("unexpected key %q", key)
		}
		if !bytes.Equal(failed.uploads[key], data) {
			t.Fatal("retry changed archive key or compressed data")
		}
		reader, errGzip := gzip.NewReader(bytes.NewReader(data))
		if errGzip != nil {
			t.Fatal(errGzip)
		}
		restored, errRead := io.ReadAll(reader)
		if errRead != nil {
			t.Fatal(errRead)
		}
		if errClose := reader.Close(); errClose != nil {
			t.Fatal(errClose)
		}
		if !bytes.Equal(restored, original) {
			t.Fatal("archive changed log contents")
		}
	}
}
func TestManagerSkipsActiveAndUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	names := []string{"main.log", ".request-log-123.pending", "response-body-123.tmp", "custom.log", "main.log.gz"}
	for _, name := range names {
		writeFixture(t, filepath.Join(dir, name), []byte(name))
	}
	uploader := &recordingUploader{}
	if err := testWorker(dir, uploader).scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != 0 {
		t.Fatal("active file was uploaded")
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestManagerDoesNotReclaimChangedSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main-2026-10-13T18-00-00.000.log")
	writeFixture(t, path, []byte("before"))
	uploader := &recordingUploader{onUpload: func() { writeFixture(t, path, []byte("after with more content")) }}
	if err := testWorker(dir, uploader).scan(context.Background()); err == nil {
		t.Fatal("expected changed source failure")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "after with more content" {
		t.Fatalf("changed log not preserved: %s %v", raw, err)
	}
}
func TestManagerRecoversOnlyPriorSessionArtifacts(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, ".request-log-old.pending")
	writeFixture(t, orphan, []byte("incomplete prior request"))
	partsDir := filepath.Join(dir, "request-log-parts-old")
	if err := os.Mkdir(partsDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(partsDir, "part-1.tmp"), []byte("prior detail"))
	RegisterDirectory(dir)
	active := filepath.Join(dir, ".request-log-current.pending")
	writeFixture(t, active, []byte("current request"))
	RegisterDirectory(dir) // Hot reload must not reclassify current-session files.
	uploader := &recordingUploader{}
	if err := testWorker(dir, uploader).scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != 2 {
		t.Fatalf("uploads = %d", len(uploader.uploads))
	}
	for key := range uploader.uploads {
		if !strings.Contains(key, "/recovered-") {
			t.Fatalf("recovery not labeled: %s", key)
		}
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active pending touched: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan not archived: %v", err)
	}
	if _, err := os.Stat(partsDir); !os.IsNotExist(err) {
		t.Fatalf("empty parts directory remains: %v", err)
	}
}
func TestManagerPreservesExistingGzipBytes(t *testing.T) {
	dir := t.TempDir()
	var archive bytes.Buffer
	zipper := gzip.NewWriter(&archive)
	if _, err := zipper.Write([]byte("existing compressed log")); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "error-v1-responses-2026-10-13T180000-id.log.gz")
	writeFixture(t, path, archive.Bytes())
	uploader := &recordingUploader{}
	if err := testWorker(dir, uploader).scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != 1 {
		t.Fatal("missing upload")
	}
	for _, data := range uploader.uploads {
		if !bytes.Equal(data, archive.Bytes()) {
			t.Fatal("existing gzip was transformed")
		}
	}
}

func TestManagerArchivesLegacyApplicationRotations(t *testing.T) {
	dir := t.TempDir()
	originals := map[string][]byte{}
	compressedOriginals := map[string][]byte{}
	for _, base := range []string{"main.log.1", "main-2026-06-15T10-00-00.log", "main-2026-06-15T10-00-00.123.log"} {
		for _, compressed := range []bool{false, true} {
			name := base
			if compressed {
				name += ".gz"
			}
			contents := []byte("complete legacy application log: " + name + "\n")
			originals[name] = contents
			stored := contents
			if compressed {
				var buffer bytes.Buffer
				zipper := gzip.NewWriter(&buffer)
				if _, err := zipper.Write(contents); err != nil {
					t.Fatal(err)
				}
				if err := zipper.Close(); err != nil {
					t.Fatal(err)
				}
				stored = buffer.Bytes()
				compressedOriginals[name] = stored
			}
			writeFixture(t, filepath.Join(dir, name), stored)
		}
	}
	excluded := []string{"main.log", "main.log.gz", "main.log.one", "main.log.1.tmp", "main-2026-99-15T10-00-00.log", "custom.log"}
	for _, name := range excluded {
		writeFixture(t, filepath.Join(dir, name), []byte("keep local"))
	}
	uploader := &recordingUploader{}
	if err := testWorker(dir, uploader).scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != len(originals) {
		t.Fatalf("uploads=%d, want %d", len(uploader.uploads), len(originals))
	}
	for name, original := range originals {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("verified rotation %s not reclaimed: %v", name, err)
		}
		archivedName := name
		if !strings.HasSuffix(archivedName, ".gz") {
			archivedName += ".gz"
		}
		found := false
		for key, raw := range uploader.uploads {
			if !strings.HasSuffix(key, "/"+archivedName) {
				continue
			}
			reader, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			restored, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(restored, original) {
				continue
			}
			if compressed, ok := compressedOriginals[name]; ok && !bytes.Equal(compressed, raw) {
				t.Fatalf("existing gzip %s changed", name)
			}
			found = true
		}
		if !found {
			t.Fatalf("lossless archive missing for %s", name)
		}
	}
	for _, name := range excluded {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(raw) != "keep local" {
			t.Fatalf("excluded file %s changed: %v", name, err)
		}
	}
}

func configureTestRegistry(t *testing.T, dir, bucket string) *directoryRegistry {
	t.Helper()
	// Keep the real configuration lifecycle, but prevent a network worker from
	// starting; the test drives its replacement uploader synchronously.
	if err := Configure(dir, config.LogArchiveConfig{Enabled: true, Bucket: bucket, Endpoint: "http://"}); err == nil {
		t.Fatal("expected invalid endpoint")
	}
	directoryState.Lock()
	registry := directoryState.current
	directoryState.Unlock()
	return registry
}

func TestManagerRetiresPriorServiceDirectories(t *testing.T) {
	Stop()
	t.Cleanup(func() {
		Stop()
		if err := Configure("", config.LogArchiveConfig{}); err != nil {
			t.Error(err)
		}
	})
	oldDir, newDir, customDir := t.TempDir(), t.TempDir(), t.TempDir()
	oldRegistry := configureTestRegistry(t, oldDir, "first-bucket")
	oldCustom := RegisterDirectory(customDir)
	oldName := "v1-responses-2026-10-13T180000-old-service.log"
	writeFixture(t, filepath.Join(oldDir, oldName), []byte("first service backlog"))
	writeFixture(t, filepath.Join(customDir, oldName), []byte("first service request backlog"))
	failed := &recordingUploader{fail: true}
	oldWorker := &manager{store: failed, directories: oldRegistry.registeredDirectories, registry: oldRegistry}
	if err := oldWorker.scan(context.Background()); err == nil {
		t.Fatal("expected first bucket failure")
	}
	Stop()
	newRegistry := configureTestRegistry(t, newDir, "second-bucket")
	// A late failure callback from the old service retains its old ownership token.
	latePath := filepath.Join(customDir, ".request-log-late.pending")
	writeFixture(t, latePath, []byte("late first service data"))
	oldCustom.Recover(latePath)
	newName := "v1-responses-2026-10-13T180000-new-service.log"
	writeFixture(t, filepath.Join(newDir, newName), []byte("second service log"))
	secondBucket := &recordingUploader{}
	newWorker := &manager{store: secondBucket, directories: newRegistry.registeredDirectories, registry: newRegistry}
	if err := newWorker.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(secondBucket.uploads) != 1 {
		t.Fatalf("second bucket received %d logs", len(secondBucket.uploads))
	}
	for key := range secondBucket.uploads {
		if !strings.HasSuffix(key, "/"+newName+".gz") {
			t.Fatalf("old service data sent to new bucket: %s", key)
		}
	}
	for _, path := range []string{filepath.Join(oldDir, oldName), filepath.Join(customDir, oldName), latePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("old service artifact touched: %s: %v", path, err)
		}
	}
}

func TestManagerDirectoryChangeScopesRegistrations(t *testing.T) {
	Stop()
	t.Cleanup(func() {
		Stop()
		if err := Configure("", config.LogArchiveConfig{}); err != nil {
			t.Error(err)
		}
	})
	firstRoot, secondRoot, requestDir := t.TempDir(), t.TempDir(), t.TempDir()
	first := configureTestRegistry(t, firstRoot, "first-bucket")
	registration := RegisterDirectory(requestDir)
	reloaded := configureTestRegistry(t, firstRoot, "first-bucket")
	if first != reloaded || len(reloaded.registeredDirectories()) != 2 {
		t.Fatal("same-root reload lost service registrations")
	}
	second := configureTestRegistry(t, secondRoot, "second-bucket")
	if first == second {
		t.Fatal("root change retained old registry")
	}
	oldPending := filepath.Join(requestDir, ".request-log-retired.pending")
	writeFixture(t, oldPending, []byte("old request"))
	registration.Recover(oldPending)
	dirs := second.registeredDirectories()
	if len(dirs) != 1 || dirs[0] != secondRoot {
		t.Fatalf("new worker inherited old directories: %v", dirs)
	}
}

func TestManagerRecoversQueuedPublicationFailureWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	registration := RegisterDirectory(dir)
	active := filepath.Join(dir, ".request-log-active.pending")
	failed := filepath.Join(dir, ".request-log-failed.pending")
	writeFixture(t, active, []byte("still writing"))
	original := []byte("completed request and response after failed publication")
	writeFixture(t, failed, original)
	// Model a failed final publication, then explicitly hand ownership of the
	// closed staging file to recovery without resnapshotting the directory.
	if err := os.Rename(failed, filepath.Join(dir, strings.Repeat("x", 300)+".log")); err == nil {
		t.Fatal("expected publication failure")
	}
	registration.Recover(failed)
	uploader := &recordingUploader{}
	worker := testWorker(dir, uploader)
	if err := worker.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != 1 {
		t.Fatalf("uploaded %d artifacts", len(uploader.uploads))
	}
	for key, raw := range uploader.uploads {
		if !strings.Contains(key, "/recovered-") {
			t.Fatalf("unexpected recovery key: %s", key)
		}
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		restored, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(restored, original) {
			t.Fatal("recovery lost pending contents")
		}
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatalf("queued artifact not archived: %v", err)
	}
	if raw, err := os.ReadFile(active); err != nil || string(raw) != "still writing" {
		t.Fatalf("active artifact touched: %v", err)
	}
}

func TestManagerReplacingRegistryDoesNotResnapshotLiveArtifacts(t *testing.T) {
	Stop()
	t.Cleanup(func() {
		Stop()
		if err := Configure("", config.LogArchiveConfig{}); err != nil {
			t.Error(err)
		}
	})
	firstRoot, otherRoot := t.TempDir(), t.TempDir()
	configureTestRegistry(t, firstRoot, "first-bucket")
	live := filepath.Join(firstRoot, ".request-log-live.pending")
	writeFixture(t, live, []byte("live first-service request"))
	configureTestRegistry(t, otherRoot, "second-bucket")
	replacement := configureTestRegistry(t, firstRoot, "third-bucket")
	uploader := &recordingUploader{}
	worker := &manager{store: uploader, directories: replacement.registeredDirectories, registry: replacement}
	if err := worker.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploader.uploads) != 0 {
		t.Fatal("new registry reclassified a live pending file")
	}
	if raw, err := os.ReadFile(live); err != nil || string(raw) != "live first-service request" {
		t.Fatalf("live file changed: %v", err)
	}
}
