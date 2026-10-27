package logging

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
	log "github.com/sirupsen/logrus"
)

// FileBodySource stores large log sections as buffered, ordered temp-file parts.
type FileBodySource struct {
	mu               sync.Mutex
	dir              string
	paths            []string
	cleaned          bool
	archiveDirectory *logarchive.Directory
	appendFile       *os.File
	appendBuffer     *bufio.Writer
	appendErr        error
	appendHasContent bool
}

const logSpoolBufferSize = 32 * 1024

// NewFileBodySourceInDir creates a temp-backed source under baseDir.
func NewFileBodySourceInDir(baseDir string, prefix string) (*FileBodySource, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, fmt.Errorf("base directory is required")
	}
	return newFileBodySourceInDir(baseDir, prefix, logarchive.RegisterDirectory(baseDir))
}

func newFileBodySourceInDir(baseDir string, prefix string, archiveDirectory *logarchive.Directory) (*FileBodySource, error) {
	prefix = sanitizeTempPrefix(prefix)
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("base directory is required")
	}
	if errMkdir := os.MkdirAll(baseDir, 0755); errMkdir != nil {
		return nil, errMkdir
	}
	dir, errCreate := os.MkdirTemp(baseDir, "request-log-parts-"+prefix+"-*")
	if errCreate != nil {
		return nil, errCreate
	}
	return &FileBodySource{dir: dir, archiveDirectory: archiveDirectory}, nil
}

func sanitizeTempPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "log"
	}
	var builder strings.Builder
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-_")
	if out == "" {
		return "log"
	}
	return out
}

// CreatePart creates one ordered detail log part.
func (s *FileBodySource) CreatePart(prefix string) (*os.File, error) {
	if s == nil {
		return nil, fmt.Errorf("file body source is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return nil, fmt.Errorf("file body source has been cleaned")
	}
	if errFinish := s.finishAppendLocked(); errFinish != nil {
		return nil, errFinish
	}
	prefix = sanitizeTempPrefix(prefix)
	if errMkdir := os.MkdirAll(s.dir, 0755); errMkdir != nil {
		return nil, errMkdir
	}
	file, errCreate := os.CreateTemp(s.dir, prefix+"-*.tmp")
	if errCreate != nil {
		return nil, errCreate
	}
	s.paths = append(s.paths, file.Name())
	return file, nil
}

// AppendPart appends one complete ordered part to the source.
func (s *FileBodySource) AppendPart(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if s == nil {
		return fmt.Errorf("file body source is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve support for a manually removed log directory without creating a
	// separate file for every event. Raw stream appends avoid this metadata check.
	if s.appendFile != nil {
		if _, errStat := os.Stat(s.appendFile.Name()); os.IsNotExist(errStat) {
			_ = s.appendFile.Close()
			s.appendFile, s.appendBuffer = nil, nil
			s.appendHasContent = false
		}
	}
	writer, errOpen := s.appendWriterLocked()
	if errOpen != nil {
		return errOpen
	}
	// WriteTo inserts a newline between physical parts. Add it here instead
	// when logical events share the same physical part.
	errWrite := writeLogPart(writer, data, s.appendHasContent)
	s.appendHasContent = true
	return errWrite
}

// AppendBytes appends raw bytes to a single ordered part.
func (s *FileBodySource) AppendBytes(data []byte) error {
	if s == nil {
		return fmt.Errorf("file body source is nil")
	}
	if len(data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writer, errOpen := s.appendWriterLocked()
	if errOpen != nil {
		return errOpen
	}
	_, errWrite := writer.Write(data)
	s.appendHasContent = true
	return errWrite
}

// appendWriterLocked keeps one descriptor and one bounded buffer per source.
func (s *FileBodySource) appendWriterLocked() (*bufio.Writer, error) {
	if s.cleaned {
		return nil, fmt.Errorf("file body source has been cleaned")
	}
	if s.appendErr != nil {
		return nil, s.appendErr
	}
	if s.appendFile != nil {
		return s.appendBuffer, nil
	}
	if errMkdir := os.MkdirAll(s.dir, 0755); errMkdir != nil {
		return nil, errMkdir
	}
	var file *os.File
	var errOpen error
	if len(s.paths) == 0 {
		file, errOpen = os.CreateTemp(s.dir, "part-*.tmp")
	} else {
		file, errOpen = os.OpenFile(s.paths[len(s.paths)-1], os.O_WRONLY|os.O_APPEND, 0600)
		if os.IsNotExist(errOpen) {
			file, errOpen = os.CreateTemp(s.dir, "part-*.tmp")
		}
	}
	if errOpen != nil {
		return nil, errOpen
	}
	if len(s.paths) == 0 || s.paths[len(s.paths)-1] != file.Name() {
		s.paths = append(s.paths, file.Name())
	}
	info, errStat := file.Stat()
	if errStat != nil {
		_ = file.Close()
		return nil, errStat
	}
	s.appendHasContent = info.Size() > 0
	s.appendFile = file
	s.appendBuffer = bufio.NewWriterSize(file, logSpoolBufferSize)
	return s.appendBuffer, nil
}

func (s *FileBodySource) finishAppendLocked() error {
	if s.appendFile != nil {
		s.appendErr = errors.Join(s.appendErr, s.appendBuffer.Flush(), s.appendFile.Close())
		s.appendFile, s.appendBuffer = nil, nil
	}
	return s.appendErr
}

// Finish flushes and closes the owned append stream before reading or recovery.
// External files returned by CreatePart remain the caller's responsibility.
func (s *FileBodySource) Finish() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishAppendLocked()
}

// HasPayload reports whether any detail parts were recorded.
func (s *FileBodySource) HasPayload() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths) > 0 && !s.cleaned
}

// Paths returns a copy of the ordered part paths.
func (s *FileBodySource) Paths() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.paths))
	copy(out, s.paths)
	return out
}

// WriteTo merges all ordered parts into w.
func (s *FileBodySource) WriteTo(w io.Writer) (int64, error) {
	if s == nil || w == nil {
		return 0, nil
	}
	if errFinish := s.Finish(); errFinish != nil {
		return 0, errFinish
	}
	var totalWritten int64
	paths := s.Paths()
	wrote := false
	for _, path := range paths {
		file, errOpen := os.Open(path)
		if errOpen != nil {
			if os.IsNotExist(errOpen) {
				continue
			}
			return totalWritten, errOpen
		}
		if wrote {
			n, errWrite := io.WriteString(w, "\n")
			totalWritten += int64(n)
			if errWrite != nil {
				if errClose := file.Close(); errClose != nil {
					log.WithError(errClose).Warn("failed to close log part file")
				}
				return totalWritten, errWrite
			}
		}
		n, errCopy := io.Copy(w, file)
		totalWritten += n
		if errClose := file.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close log part file")
			if errCopy == nil {
				errCopy = errClose
			}
		}
		if errCopy != nil {
			return totalWritten, errCopy
		}
		wrote = true
	}
	return totalWritten, nil
}

// Bytes merges all ordered parts into memory.
func (s *FileBodySource) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if _, errWrite := s.WriteTo(&buf); errWrite != nil {
		return nil, errWrite
	}
	return buf.Bytes(), nil
}

// Cleanup removes all temp detail parts and their directory.
func (s *FileBodySource) Cleanup() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.cleaned {
		s.mu.Unlock()
		return nil
	}
	errFinish := s.finishAppendLocked()
	paths := make([]string, len(s.paths))
	copy(paths, s.paths)
	dir := s.dir
	s.paths = nil
	s.cleaned = true
	s.mu.Unlock()

	firstErr := errFinish
	for _, path := range paths {
		if errRemove := os.Remove(path); errRemove != nil && !os.IsNotExist(errRemove) && firstErr == nil {
			firstErr = errRemove
		}
	}
	if dir != "" {
		if errRemove := os.RemoveAll(dir); errRemove != nil && firstErr == nil {
			firstErr = errRemove
		}
	}
	return firstErr
}

func cleanupFileBodySources(sources ...*FileBodySource) {
	for _, source := range sources {
		if source == nil {
			continue
		}
		if errCleanup := source.Cleanup(); errCleanup != nil {
			log.WithError(errCleanup).Warn("failed to clean up log part files")
		}
	}
}

// recoverFileBodySources transfers only finalized sources to their owning service.
func recoverFileBodySources(sources ...*FileBodySource) {
	for _, source := range sources {
		if source != nil {
			source.Recover()
		}
	}
}

// Recover queues finalized source parts for lossless archival after a logging failure.
// All producers must have stopped writing before this method is called.
func (s *FileBodySource) Recover() {
	if s != nil {
		if errFinish := s.Finish(); errFinish != nil {
			log.WithError(errFinish).Warn("failed to flush recovered log source")
		}
		s.archiveDirectory.Recover(s.Paths()...)
	}
}
