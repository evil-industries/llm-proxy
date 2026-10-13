package logging

import (
	"testing"
	"testing/synctest"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

func TestStreamingWriterLatchesHotEnabledArchival(t *testing.T) {
	if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	t.Cleanup(func() {
		if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
			t.Error(errConfigure)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		writer := &FileStreamingLogWriter{chunkChan: make(chan []byte, 1)}
		for _, enabled := range []bool{true, false} {
			// Missing bucket enables conservative preservation without starting
			// a network worker. The second pass verifies the latch after disable.
			errConfigure := logarchive.Configure("", config.LogArchiveConfig{Enabled: enabled})
			if (errConfigure != nil) != enabled {
				t.Fatalf("Configure(enabled=%v): %v", enabled, errConfigure)
			}
			writer.chunkChan <- []byte("already queued")
			done := make(chan struct{})
			go func() {
				defer close(done)
				writer.WriteChunkAsync([]byte("preserved after reload"))
			}()
			synctest.Wait()
			select {
			case <-done:
				t.Fatalf("chunk dropped after reload with archival enabled=%v", enabled)
			default:
			}
			<-writer.chunkChan
			<-done
			if got := string(<-writer.chunkChan); got != "preserved after reload" {
				t.Fatalf("captured chunk = %q", got)
			}
		}
	})
}

func TestStreamingWriterRetainsPropagatedPreservation(t *testing.T) {
	writer := &FileStreamingLogWriter{}
	writer.PreserveLogs()
	if !writer.preservingLogs() {
		t.Fatal("downstream queue ignored request preservation policy")
	}
}
