package watcher

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestConfigCallbacksAreSerializedAcrossReloadPaths(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(configPath, []byte("api-keys: [anchor]\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	var active atomic.Int32
	var callbacks atomic.Int32
	var overlapping atomic.Bool
	w := &Watcher{configPath: configPath}
	w.SetConfig(&config.Config{AuthDir: t.TempDir()})
	w.reloadCallback = func(*config.Config) {
		if active.Add(1) != 1 {
			overlapping.Store(true)
		}
		callbacks.Add(1)
		// Offer other ready reloads a chance to enter while this callback is active.
		runtime.Gosched()
		active.Add(-1)
	}
	const workers = 24
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			switch i % 3 {
			case 0:
				w.reloadConfig()
			case 1:
				w.reloadClients(false, nil, false)
			case 2:
				w.reloadLatestConfig(nil)
			}
		}()
	}
	close(start)
	group.Wait()
	if overlapping.Load() {
		t.Fatal("configuration callbacks overlapped")
	}
	if callbacks.Load() != workers {
		t.Fatalf("callbacks = %d, want %d", callbacks.Load(), workers)
	}
}

func TestImmediateServerUpdateUsesCurrentConfig(t *testing.T) {
	oldConfig := &config.Config{Port: 8080}
	currentConfig := &config.Config{Port: 9090}
	var applied *config.Config
	w := &Watcher{reloadCallback: func(cfg *config.Config) { applied = cfg }}
	w.SetConfig(currentConfig)
	w.triggerServerUpdate(oldConfig)
	if applied != currentConfig {
		t.Fatal("server update applied a stale captured configuration")
	}
}
