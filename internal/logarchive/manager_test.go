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
	return &manager{store: uploader, directories: func() []string { return []string{dir} }}
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
