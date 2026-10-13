package logarchive

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

// Only immutable, completed logs are archived. Request writers publish closed
// files by rename; lumberjack closes the application log before rotating it.
const scanInterval = time.Minute

var completedLogPattern = regexp.MustCompile(`^(main-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}\.[0-9]{3}|.+-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{6}-.+|recovered-.+)\.log(\.gz)?$`)

// IsCompletedLogName recognizes immutable files emitted by the log writers.
func IsCompletedLogName(name string) bool { return completedLogPattern.MatchString(name) }

type verifiedUploader interface {
	UploadVerified(context.Context, string, string) error
}
type manager struct {
	store       verifiedUploader
	directories func() []string
}

var lifecycle struct {
	sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}
var enabled atomic.Bool
var retentionGate sync.RWMutex

// RetentionGuard excludes configuration changes while a destructive retention pass runs.
// Call the returned release function even when allowed is false.
func RetentionGuard() (release func(), allowed bool) {
	retentionGate.RLock()
	return retentionGate.RUnlock, !Enabled()
}

var directories = struct {
	sync.Mutex
	paths map[string][]string
}{paths: make(map[string][]string)}

// Enabled reports whether local logs must be preserved for archival.
func Enabled() bool { return enabled.Load() }

// RegisterDirectory includes request logs stored outside the application log directory.
func RegisterDirectory(dir string) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	directories.Lock()
	if _, exists := directories.paths[absolute]; !exists {
		// Registration precedes creation of any per-request files. Snapshot only
		// once, so enabling archival on reload cannot mistake live files for debris.
		directories.paths[absolute] = orphanedFiles(absolute)
	}
	directories.Unlock()
}

func orphanedFiles(dir string) []string {
	var paths []string
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() && strings.HasPrefix(name, "request-log-parts-") {
			parts, _ := os.ReadDir(path)
			for _, part := range parts {
				info, err := part.Info()
				if err == nil && info.Mode().IsRegular() {
					paths = append(paths, filepath.Join(path, part.Name()))
				}
			}
			continue
		}
		known := (strings.HasPrefix(name, ".request-log-") && strings.HasSuffix(name, ".pending")) ||
			((strings.HasPrefix(name, "request-body-") || strings.HasPrefix(name, "response-body-") || strings.HasPrefix(name, ".log-archive-")) && strings.HasSuffix(name, ".tmp"))
		info, err := entry.Info()
		if known && err == nil && info.Mode().IsRegular() {
			paths = append(paths, path)
		}
	}
	return paths
}

func recoverOrphans(dir string) error {
	directories.Lock()
	defer directories.Unlock()
	pending := directories.paths[dir]
	var failures []error
	var remaining []string
	for _, source := range pending {
		name := "recovered-" + filepath.Base(source) + "-" + uuid.NewString() + ".log"
		if errRename := os.Rename(source, filepath.Join(dir, name)); errRename != nil {
			if !os.IsNotExist(errRename) {
				failures = append(failures, errRename)
				remaining = append(remaining, source)
			}
			continue
		}
		parent := filepath.Dir(source)
		if parent != dir {
			// Remove only an empty parts directory; never recursively remove data.
			_ = os.Remove(parent)
		}
	}
	directories.paths[dir] = remaining
	return errors.Join(failures...)
}

func registeredDirectories() []string {
	directories.Lock()
	defer directories.Unlock()
	result := make([]string, 0, len(directories.paths))
	for dir := range directories.paths {
		result = append(result, dir)
	}
	return result
}

// Configure replaces the worker. Invalid settings preserve local files and stop
// the previous worker rather than sending logs to a stale destination.
func Configure(dir string, cfg config.LogArchiveConfig) error {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	retentionGate.Lock()
	enabled.Store(cfg.Enabled)
	retentionGate.Unlock()
	stopLocked()
	if !cfg.Enabled {
		return nil
	}
	store, err := NewStore(cfg)
	if err != nil {
		return err
	}
	RegisterDirectory(dir)
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle.cancel = cancel
	lifecycle.done = make(chan struct{})
	done := lifecycle.done
	worker := &manager{store: store, directories: registeredDirectories}
	go func() {
		defer close(done)
		defer store.Close()
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()
		for {
			if errScan := worker.scan(ctx); errScan != nil && ctx.Err() == nil {
				// SDK errors may contain endpoint URLs or deployment credentials.
				log.Warn("log archive: archival failed; local logs preserved and upload will be retried")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

// Stop cancels network work and waits until local files are no longer touched.
// Retention remains disabled until Configure explicitly disables archival.
func Stop() {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	stopLocked()
}
func stopLocked() {
	if lifecycle.cancel != nil {
		lifecycle.cancel()
		<-lifecycle.done
		lifecycle.cancel = nil
		lifecycle.done = nil
	}
}
func (m *manager) scan(ctx context.Context) error {
	var firstError error
	record := func(err error) {
		if firstError == nil {
			firstError = err
		}
	}
	for _, dir := range m.directories() {
		record(recoverOrphans(dir))
		folder, errOpen := os.Open(dir)
		if os.IsNotExist(errOpen) {
			continue
		}
		if errOpen != nil {
			record(errOpen)
			continue
		}
		for {
			entries, errRead := folder.ReadDir(100)
			for _, entry := range entries {
				if errContext := ctx.Err(); errContext != nil {
					_ = folder.Close()
					return errContext
				}
				name := entry.Name()
				if !IsCompletedLogName(name) {
					continue
				}
				info, errInfo := entry.Info()
				if errInfo != nil || !info.Mode().IsRegular() {
					continue
				}
				record(m.archive(ctx, filepath.Join(dir, name)))
			}
			if errRead != nil {
				if !errors.Is(errRead, io.EOF) {
					record(errRead)
				}
				break
			}
		}
		record(folder.Close())
	}
	return firstError
}
func (m *manager) archive(ctx context.Context, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	inputClosed := false
	defer func() {
		if inputClosed {
			return
		}
		if errClose := input.Close(); errClose != nil {
			log.Warn("log archive: failed to close source file")
		}
	}()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("archive source is not a regular file")
	}
	archivePath := source
	archiveName := filepath.Base(source)
	if !strings.HasSuffix(source, ".gz") {
		compressed, errCreate := os.CreateTemp(filepath.Dir(source), ".log-archive-*.tmp")
		if errCreate != nil {
			return errCreate
		}
		archivePath = compressed.Name()
		defer func() {
			if errRemove := os.Remove(archivePath); errRemove != nil && !os.IsNotExist(errRemove) {
				log.Warn("log archive: failed to remove compression staging file")
			}
		}()
		writer := gzip.NewWriter(compressed)
		_, errCopy := copyWithContext(ctx, writer, input)
		errGzip := writer.Close()
		errSync := compressed.Sync()
		errClose := compressed.Close()
		if errWrite := errors.Join(errCopy, errGzip, errSync, errClose); errWrite != nil {
			return errWrite
		}
		archiveName += ".gz"
	}
	archiveFile, errOpen := os.Open(archivePath)
	if errOpen != nil {
		return errOpen
	}
	digest := sha256.New()
	_, errHash := copyWithContext(ctx, digest, archiveFile)
	errClose := archiveFile.Close()
	if errRead := errors.Join(errHash, errClose); errRead != nil {
		return errRead
	}
	if errCloseSource := input.Close(); errCloseSource != nil {
		return errCloseSource
	}
	inputClosed = true
	key := hex.EncodeToString(digest.Sum(nil)) + "/" + archiveName
	if errUpload := m.store.UploadVerified(ctx, key, archivePath); errUpload != nil {
		return errUpload
	}
	// Never reclaim a replaced/changed source using a previous upload.
	after, errStat := os.Stat(source)
	if errStat != nil {
		return errStat
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("archive source changed during upload")
	}
	if errContext := ctx.Err(); errContext != nil {
		return errContext
	}
	return os.Remove(source)
}
