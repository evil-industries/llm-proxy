package logging

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
	log "github.com/sirupsen/logrus"
)

func TestDisablingFileLoggingPublishesFinalArchiveSegment(t *testing.T) {
	restoreApplicationLogTestGlobals(t)
	dir := t.TempDir()
	t.Setenv("WRITABLE_PATH", dir)
	previousOutput := log.StandardLogger().Out
	t.Cleanup(func() {
		if errConfigure := ConfigureLogOutput(&config.Config{}); errConfigure != nil {
			t.Error(errConfigure)
		}
		log.SetOutput(previousOutput)
	})
	cfg := &config.Config{LoggingToFile: true}
	if errConfigure := ConfigureLogOutput(cfg); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	log.Info("last application segment before stdout")
	mainPath := filepath.Join(dir, "logs", "main.log")
	want, errRead := os.ReadFile(mainPath)
	if errRead != nil {
		t.Fatal(errRead)
	}
	uploaded := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			select {
			case uploaded <- r.URL.Path:
			default:
			}
		}
		// Preserve the completed local segment while proving the worker sees it.
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer server.Close()
	cfg.LoggingToFile = false
	cfg.LogArchive = config.LogArchiveConfig{Enabled: true, Endpoint: server.URL, Bucket: "archive-test", Region: "us-east-1", AccessKeyID: "test", SecretAccessKey: "test", PathStyle: true}
	if errConfigure := ConfigureLogOutput(cfg); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	select {
	case path := <-uploaded:
		if !strings.Contains(path, "/main-") || !strings.HasSuffix(path, ".log.gz") {
			t.Fatalf("unexpected uploaded final segment %q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closed application segment was not submitted for archival")
	}
	logarchive.Stop()
	if _, errStat := os.Stat(mainPath); !os.IsNotExist(errStat) {
		t.Fatalf("closed segment remains under active filename: %v", errStat)
	}
	entries, errList := os.ReadDir(filepath.Dir(mainPath))
	if errList != nil {
		t.Fatal(errList)
	}
	if len(entries) != 1 || !logarchive.IsCompletedLogName(entries[0].Name()) {
		t.Fatalf("expected one completed application segment, got %v", entries)
	}
	got, errRead := os.ReadFile(filepath.Join(filepath.Dir(mainPath), entries[0].Name()))
	if errRead != nil || string(got) != string(want) {
		t.Fatalf("final segment changed: %q, %v", got, errRead)
	}
}

func TestDisabledFileLoggingRecoversPreviousProcessMainLog(t *testing.T) {
	restoreApplicationLogTestGlobals(t)
	dir := t.TempDir()
	t.Setenv("WRITABLE_PATH", dir)
	if errConfigure := ConfigureLogOutput(&config.Config{}); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	t.Cleanup(func() {
		if errConfigure := ConfigureLogOutput(&config.Config{}); errConfigure != nil {
			t.Error(errConfigure)
		}
	})
	logDir := filepath.Join(dir, "logs")
	if errMkdir := os.MkdirAll(logDir, 0700); errMkdir != nil {
		t.Fatal(errMkdir)
	}
	mainPath := filepath.Join(logDir, "main.log")
	if errWrite := os.WriteFile(mainPath, []byte("previous process log"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	// Invalid storage settings stop before uploads, preserving the published
	// completed segment for inspection and a subsequent valid configuration.
	if errConfigure := ConfigureLogOutput(&config.Config{LogArchive: config.LogArchiveConfig{Enabled: true}}); errConfigure == nil {
		t.Fatal("expected missing-bucket validation error")
	}
	entries, errRead := os.ReadDir(logDir)
	if errRead != nil || len(entries) != 1 || !logarchive.IsCompletedLogName(entries[0].Name()) {
		t.Fatalf("closed main.log was not recovered: %v, %v", entries, errRead)
	}
	payload, errRead := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if errRead != nil || string(payload) != "previous process log" {
		t.Fatalf("recovered segment changed: %q, %v", payload, errRead)
	}
}

// ConfigureLogOutput initializes asynchronous Gin pipes. Restore the original
// writers so later recovery tests cannot enqueue unrelated entries into log hooks.
func restoreApplicationLogTestGlobals(t *testing.T) {
	t.Helper()
	infoWriter, errorWriter, debugPrint := gin.DefaultWriter, gin.DefaultErrorWriter, gin.DebugPrintFunc
	logger := log.StandardLogger()
	output, formatter, reportCaller := logger.Out, logger.Formatter, logger.ReportCaller
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter, gin.DebugPrintFunc = infoWriter, errorWriter, debugPrint
		log.SetOutput(output)
		log.SetFormatter(formatter)
		log.SetReportCaller(reportCaller)
	})
}
