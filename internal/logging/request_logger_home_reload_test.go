package logging

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

type hotEnableHomeLogClient struct {
	t       *testing.T
	err     error
	calls   int
	payload []byte
}

func (c *hotEnableHomeLogClient) HeartbeatOK() bool { return true }

func (c *hotEnableHomeLogClient) RPushRequestLog(_ context.Context, payload []byte) error {
	c.calls++
	c.payload = bytes.Clone(payload)
	enableArchivePreservation(c.t)
	return c.err
}

func TestHomeNonStreamingHotEnablePublishesCompleteLocalArchive(t *testing.T) {
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	for _, forwardFails := range []bool{false, true} {
		name := "forward succeeds"
		if forwardFails {
			name = "forward fails"
		}
		t.Run(name, func(t *testing.T) {
			if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
				t.Fatal(errConfigure)
			}
			client := &hotEnableHomeLogClient{t: t}
			if forwardFails {
				client.err = errors.New("injected Home forwarding failure")
			}
			currentHomeRequestLogClient = func() homeRequestLogClient { return client }
			dir := t.TempDir()
			logger := NewFileRequestLogger(true, dir, "", 0)
			logger.SetHomeEnabled(true)
			var sources []*FileBodySource
			var paths []string
			for _, payload := range []string{"stored request detail", "stored response detail", "stored upstream websocket detail"} {
				source, errSource := logger.NewFileBodySource("hot-enable")
				if errSource != nil {
					t.Fatal(errSource)
				}
				if errAppend := source.AppendPart([]byte(payload)); errAppend != nil {
					t.Fatal(errAppend)
				}
				sources = append(sources, source)
				paths = append(paths, source.Paths()...)
			}
			errLog := logger.LogRequestWithOptionsAndAllSources(
				"/v1/responses", "POST", nil, []byte("memory request body"), 200, nil,
				[]byte("memory response body"), nil, nil,
				[]byte("memory upstream request"), sources[0], []byte("memory upstream response"), sources[1],
				[]byte("memory upstream websocket"), sources[2], nil, false, "reload-home", time.Now(), time.Time{},
			)
			if errLog != nil {
				t.Fatal(errLog)
			}
			if client.calls != 1 {
				t.Fatalf("Home forwarding attempts = %d, want one", client.calls)
			}
			entries, errRead := os.ReadDir(dir)
			if errRead != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-reload-home.log") {
				t.Fatalf("completed local archive = %v, %v", entries, errRead)
			}
			content, errRead := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			if errRead != nil {
				t.Fatal(errRead)
			}
			for _, expected := range []string{
				"memory request body", "memory response body", "memory upstream request", "memory upstream response", "memory upstream websocket",
				"stored request detail", "stored response detail", "stored upstream websocket detail",
			} {
				if !bytes.Contains(content, []byte(expected)) {
					t.Fatalf("local archive omitted %q", expected)
				}
				if !bytes.Contains(client.payload, []byte(expected)) {
					t.Fatalf("Home payload omitted %q", expected)
				}
			}
			assertFileBodySourceCleaned(t, paths)
		})
	}
}
