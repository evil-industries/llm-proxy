package logging

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	setupOnce      sync.Once
	writerMu       sync.Mutex
	logWriter      *lumberjack.Logger
	ginInfoWriter  *io.PipeWriter
	ginErrorWriter *io.PipeWriter
)

// LogFormatter defines a custom log format for logrus.
// This formatter adds timestamp, level, request ID, and source location to each log entry.
// Format: [2025-12-23 20:14:04] [debug] [manager.go:524] | a1b2c3d4 | Use API key sk-9...0RHO for model gpt-5.2
type LogFormatter struct{}

// logFieldOrder defines the display order for common log fields.
var logFieldOrder = []string{
	"provider", "model", "thread_id", "account_id",
	"plugin_id", "plugin_name", "source_id",
	"version", "active_version", "retired_version", "overwritten",
	"mode", "budget", "level", "original_mode", "original_value", "min", "max", "clamped_to", "error",
	"credential", "connection", "proxy_scheme", "remote_transport",
	"media_session_id", "call_id", "peer", "state", "reason",
}

var quotedLogFields = map[string]struct{}{
	"thread_id":        {},
	"account_id":       {},
	"credential":       {},
	"connection":       {},
	"proxy_scheme":     {},
	"remote_transport": {},
	"media_session_id": {},
	"call_id":          {},
	"peer":             {},
	"state":            {},
	"reason":           {},
}

var pluginPathFieldOrder = []string{"path", "active_path", "retired_path"}

func formatLogFieldValue(key string, value any) string {
	if _, quoted := quotedLogFields[key]; quoted {
		if stringValue, ok := value.(string); ok {
			return strconv.Quote(stringValue)
		}
	}
	return fmt.Sprint(value)
}

// Format renders a single log entry with custom formatting.
func (m *LogFormatter) Format(entry *log.Entry) ([]byte, error) {
	var buffer *bytes.Buffer
	if entry.Buffer != nil {
		buffer = entry.Buffer
	} else {
		buffer = &bytes.Buffer{}
	}

	timestamp := entry.Time.Format("2006-01-02 15:04:05Z07:00")
	message := strings.TrimRight(entry.Message, "\r\n")

	reqID := "--------"
	if id, ok := entry.Data["request_id"].(string); ok && id != "" {
		reqID = id
	}

	level := entry.Level.String()
	if level == "warning" {
		level = "warn"
	}
	levelStr := fmt.Sprintf("%-5s", level)

	// Build fields string (only print fields in logFieldOrder)
	var fieldsStr string
	if len(entry.Data) > 0 {
		var fields []string
		for _, k := range logFieldOrder {
			if v, ok := entry.Data[k]; ok {
				fields = append(fields, fmt.Sprintf("%s=%s", k, formatLogFieldValue(k, v)))
			}
		}
		if pluginID, ok := entry.Data["plugin_id"]; ok && strings.TrimSpace(fmt.Sprint(pluginID)) != "" {
			for _, k := range pluginPathFieldOrder {
				if v, ok := entry.Data[k]; ok {
					fields = append(fields, fmt.Sprintf("%s=%v", k, v))
				}
			}
		}
		if len(fields) > 0 {
			fieldsStr = " " + strings.Join(fields, " ")
		}
	}

	var formatted string
	if entry.Caller != nil {
		formatted = fmt.Sprintf("[%s] [%s] [%s] [%s:%d] %s%s\n", timestamp, reqID, levelStr, filepath.Base(entry.Caller.File), entry.Caller.Line, message, fieldsStr)
	} else {
		formatted = fmt.Sprintf("[%s] [%s] [%s] %s%s\n", timestamp, reqID, levelStr, message, fieldsStr)
	}
	buffer.WriteString(formatted)

	return buffer.Bytes(), nil
}

// SetupBaseLogger configures the shared logrus instance and Gin writers.
// It is safe to call multiple times; initialization happens only once.
func SetupBaseLogger() {
	setupOnce.Do(func() {
		log.SetOutput(os.Stdout)
		log.SetReportCaller(true)
		log.SetFormatter(&LogFormatter{})

		ginInfoWriter = log.StandardLogger().Writer()
		gin.DefaultWriter = ginInfoWriter
		ginErrorWriter = log.StandardLogger().WriterLevel(log.ErrorLevel)
		gin.DefaultErrorWriter = ginErrorWriter
		gin.DebugPrintFunc = func(format string, values ...interface{}) {
			format = strings.TrimRight(format, "\r\n")
			log.StandardLogger().Infof(format, values...)
		}

		log.RegisterExitHandler(closeLogOutputs)
	})
}

// isDirWritable checks if the specified directory exists and is writable by attempting to create and remove a test file.
func isDirWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}

	testFile := filepath.Join(dir, ".perm_test")
	f, err := os.Create(testFile)
	if err != nil {
		return false
	}

	defer func() {
		_ = f.Close()
		_ = os.Remove(testFile)
	}()
	return true
}

// ResolveLogDirectory determines the directory used for application logs.
func ResolveLogDirectory(cfg *config.Config) string {
	logDir := "logs"
	if base := util.WritablePath(); base != "" {
		return filepath.Join(base, "logs")
	}
	if cfg == nil {
		return logDir
	}
	if !isDirWritable(logDir) {
		authDir, err := util.ResolveAuthDir(cfg.AuthDir)
		if err != nil {
			log.Warnf("Failed to resolve auth-dir %q for log directory: %v", cfg.AuthDir, err)
		}
		if authDir != "" {
			logDir = filepath.Join(authDir, "logs")
		}
	}
	return logDir
}

// ConfigureLogOutput switches the global log destination between rotating files and stdout.
// When logsMaxTotalSizeMB > 0, a background cleaner removes the oldest log files in the logs directory
// until the total size is within the limit.
func ConfigureLogOutput(cfg *config.Config) (errConfigure error) {
	SetupBaseLogger()

	writerMu.Lock()
	defer writerMu.Unlock()

	logDir := ResolveLogDirectory(cfg)

	protectedPath := ""
	// A reload must stop the old destination before publishing any new segment.
	// Keep directory ownership intact so in-flight request loggers survive reload.
	stopLogDirCleanerLocked()
	logarchive.Quiesce()
	defer func() {
		if errArchive := logarchive.Configure(logDir, cfg.LogArchive); errArchive != nil {
			errConfigure = errors.Join(errConfigure, fmt.Errorf("logging: configure archive: %w", errArchive))
		}
		if errConfigure == nil {
			configureLogDirCleanerLocked(logDir, cfg.LogsMaxTotalSizeMB, protectedPath)
		}
	}()
	if cfg.LoggingToFile {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("logging: failed to create log directory: %w", err)
		}
		if logWriter != nil {
			_ = logWriter.Close()
		}
		protectedPath = filepath.Join(logDir, "main.log")
		logWriter = &lumberjack.Logger{
			Filename:   protectedPath,
			MaxSize:    10,
			MaxBackups: 0,
			MaxAge:     0,
			Compress:   false,
		}
		log.SetOutput(eventLogWriter{logWriter})
	} else {
		// SetOutput serializes against in-flight logrus writes. The old writer
		// must have no remaining producers when its final segment is published.
		log.SetOutput(os.Stdout)
		closedPath := filepath.Join(logDir, "main.log")
		if logWriter != nil {
			closedPath = logWriter.Filename
			if errClose := logWriter.Close(); errClose != nil {
				return fmt.Errorf("logging: close application log: %w", errClose)
			}
			logWriter = nil
		}
		if cfg.LogArchive.Enabled {
			// This also recovers a previous process's final main.log when the
			// replacement process starts with file logging disabled.
			if errPublish := publishClosedApplicationLog(closedPath); errPublish != nil {
				return errPublish
			}
		}
	}

	return nil
}

func publishClosedApplicationLog(path string) error {
	info, errStat := os.Lstat(path)
	if os.IsNotExist(errStat) {
		return nil
	}
	if errStat != nil {
		return fmt.Errorf("logging: inspect final application log: %w", errStat)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("logging: final application log is not a regular file")
	}
	name := "main-" + time.Now().Format("2006-01-02T15-04-05.000") + "." + uuid.NewString() + ".log"
	if errRename := os.Rename(path, filepath.Join(filepath.Dir(path), name)); errRename != nil {
		return fmt.Errorf("logging: publish final application log: %w", errRename)
	}
	return nil
}

func closeLogOutputs() {
	WaitForRequestLogs()
	writerMu.Lock()
	defer writerMu.Unlock()

	stopLogDirCleanerLocked()
	logarchive.Stop()

	if logWriter != nil {
		_ = logWriter.Close()
		logWriter = nil
	}
	if ginInfoWriter != nil {
		_ = ginInfoWriter.Close()
		ginInfoWriter = nil
	}
	if ginErrorWriter != nil {
		_ = ginErrorWriter.Close()
		ginErrorWriter = nil
	}
}

// Notify only after bytes reach disk, so cursor reads can immediately see them.
type eventLogWriter struct{ io.Writer }

func (w eventLogWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		managementevents.Publish(managementevents.Logs)
	}
	return n, err
}
