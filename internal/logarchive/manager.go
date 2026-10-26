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

type verifiedUploader interface {
	UploadVerified(context.Context, string, string) error
}
type manager struct {
	store       verifiedUploader
	directories func() []string
	registry    *directoryRegistry
	compressor  *gzip.Writer
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

type directoryRegistry struct {
	sync.Mutex
	root  string
	paths map[string][]string
}

// Initial snapshots are process-wide discovery metadata, not worker registrations.
// Reusing a directory after reload/Stop must never reclassify live process files.
var initialSnapshots = struct {
	sync.Mutex
	paths map[string][]string
}{paths: make(map[string][]string)}

var directoryState = struct {
	sync.Mutex
	current *directoryRegistry
}{current: newDirectoryRegistry()}

func newDirectoryRegistry() *directoryRegistry {
	return &directoryRegistry{paths: make(map[string][]string)}
}

// Directory ties a request logger's recovery work to its originating service.
// A late completion from a retired service cannot register files in its successor.
type Directory struct {
	registry *directoryRegistry
	path     string
}

// Enabled reports whether local logs must be preserved for archival.
func Enabled() bool { return enabled.Load() }

// RegisterDirectory includes an explicitly owned request directory in this service.
// Call before creating any request files so the initial orphan snapshot is safe.
func RegisterDirectory(dir string) *Directory {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	directoryState.Lock()
	defer directoryState.Unlock()
	registry := directoryState.current
	registry.register(absolute)
	return &Directory{registry: registry, path: absolute}
}

func (r *directoryRegistry) register(dir string) {
	r.Lock()
	defer r.Unlock()
	if _, exists := r.paths[dir]; !exists {
		initialSnapshots.Lock()
		snapshot, seen := initialSnapshots.paths[dir]
		if !seen {
			snapshot = orphanedFiles(dir)
			initialSnapshots.paths[dir] = snapshot
		}
		r.paths[dir] = append([]string(nil), snapshot...)
		initialSnapshots.Unlock()
	}
}

// Recover queues closed artifacts from a failed request for the next scan.
// It must never be called while a producer can still write to these files.
func (d *Directory) Recover(paths ...string) {
	if d == nil {
		return
	}
	d.registry.Lock()
	defer d.registry.Unlock()
	for _, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(d.path, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		d.registry.paths[d.path] = append(d.registry.paths[d.path], absolute)
	}
}

func configureDirectories(dir string) *directoryRegistry {
	absolute := ""
	if dir != "" {
		absolute, _ = filepath.Abs(dir)
	}
	directoryState.Lock()
	defer directoryState.Unlock()
	registry := directoryState.current
	if registry.root != "" && registry.root != absolute {
		registry = newDirectoryRegistry()
		directoryState.current = registry
	}
	registry.root = absolute
	if absolute != "" {
		registry.register(absolute)
	}
	return registry
}

func retireDirectories() {
	directoryState.Lock()
	directoryState.current = newDirectoryRegistry()
	directoryState.Unlock()
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

func (r *directoryRegistry) recoverOrphans(dir string) error {
	r.Lock()
	defer r.Unlock()
	pending, exists := r.paths[dir]
	if !exists {
		return nil
	}
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
	r.paths[dir] = remaining
	return errors.Join(failures...)
}

func (r *directoryRegistry) registeredDirectories() []string {
	r.Lock()
	defer r.Unlock()
	result := make([]string, 0, len(r.paths))
	for dir := range r.paths {
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
	registry := configureDirectories(dir)
	if !cfg.Enabled {
		return nil
	}
	store, err := NewStore(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle.cancel = cancel
	lifecycle.done = make(chan struct{})
	done := lifecycle.done
	worker := &manager{store: store, directories: registry.registeredDirectories, registry: registry}
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

// Quiesce stops uploads while preserving this service's directory ownership and
// retention guard. Configure resumes with the new destination after local writers
// have been switched, even if publishing their last segment fails.
func Quiesce() {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	stopLocked()
}

// Stop cancels network work and waits until local files are no longer touched.
// Retention remains disabled until Configure explicitly disables archival.
func Stop() {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	stopLocked()
	retireDirectories()
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
		if m.registry != nil {
			record(m.registry.recoverOrphans(dir))
		}
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
	digest := sha256.New()
	var archiveSize int64
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
		// Hash compressed bytes as they are written, avoiding a separate read.
		output := io.MultiWriter(compressed, digest)
		if m.compressor == nil {
			m.compressor = gzip.NewWriter(output)
		} else {
			m.compressor.Reset(output)
		}
		_, errCopy := copyWithContext(ctx, m.compressor, input)
		errGzip := m.compressor.Close()
		info, errStat := compressed.Stat()
		errClose := compressed.Close()
		// The original remains available until remote verification succeeds;
		// this disposable compression staging file does not need an fsync.
		if errWrite := errors.Join(errCopy, errGzip, errStat, errClose); errWrite != nil {
			return errWrite
		}
		archiveSize = info.Size()
		archiveName += ".gz"
	} else {
		var errHash error
		archiveSize, errHash = copyWithContext(ctx, digest, input)
		if errHash != nil {
			return errHash
		}
	}
	if errCloseSource := input.Close(); errCloseSource != nil {
		return errCloseSource
	}
	inputClosed = true
	key := hex.EncodeToString(digest.Sum(nil)) + "/" + archiveName
	var errUpload error
	if prepared, ok := m.store.(interface {
		uploadPrepared(context.Context, string, string, int64) error
	}); ok {
		errUpload = prepared.uploadPrepared(ctx, key, archivePath, archiveSize)
	} else {
		errUpload = m.store.UploadVerified(ctx, key, archivePath)
	}
	if errUpload != nil {
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
