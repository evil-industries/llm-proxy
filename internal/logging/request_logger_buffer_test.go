package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBufferedSourcePreservesMixedPartBoundaries(t *testing.T) {
	source, errSource := NewFileBodySourceInDir(t.TempDir(), "mixed")
	if errSource != nil {
		t.Fatal(errSource)
	}
	t.Cleanup(func() { _ = source.Cleanup() })
	if errAppend := source.AppendBytes([]byte("raw")); errAppend != nil {
		t.Fatal(errAppend)
	}
	for _, event := range []string{" first event ", "second event"} {
		if errAppend := source.AppendPart([]byte(event)); errAppend != nil {
			t.Fatal(errAppend)
		}
	}
	if len(source.Paths()) != 1 {
		t.Fatal("events were not consolidated into one spool file")
	}
	part, errPart := source.CreatePart("external")
	if errPart != nil {
		t.Fatal(errPart)
	}
	if _, errWrite := part.WriteString("external"); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errClose := part.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	if errAppend := source.AppendBytes([]byte(" tail")); errAppend != nil {
		t.Fatal(errAppend)
	}
	if errAppend := source.AppendPart([]byte("third event")); errAppend != nil {
		t.Fatal(errAppend)
	}
	contents, errRead := source.Bytes()
	want := "raw\nfirst event\n\nsecond event\n\nexternal tail\nthird event\n"
	if errRead != nil || string(contents) != want {
		t.Fatalf("contents = %q, %v; want %q", contents, errRead, want)
	}
}

func TestBufferedSourceRecoveryFlushesAndClosesBeforePublication(t *testing.T) {
	source, errSource := NewFileBodySourceInDir(t.TempDir(), "recover")
	if errSource != nil {
		t.Fatal(errSource)
	}
	t.Cleanup(func() { _ = source.Cleanup() })
	payload := []byte("captured bytes smaller than the write buffer")
	if errAppend := source.AppendBytes(payload); errAppend != nil {
		t.Fatal(errAppend)
	}
	file := source.appendFile
	if info, errStat := file.Stat(); errStat != nil || info.Size() != 0 {
		t.Fatalf("small write was not buffered: %v, %v", info, errStat)
	}
	source.Recover()
	if _, errStat := file.Stat(); errStat == nil {
		t.Fatal("recovery left the writer open")
	}
	data, errRead := os.ReadFile(source.Paths()[0])
	if errRead != nil || !bytes.Equal(data, payload) {
		t.Fatalf("recovered bytes = %q, %v", data, errRead)
	}
}

func TestBufferedSourceReportsFinalFlushFailure(t *testing.T) {
	source, errSource := NewFileBodySourceInDir(t.TempDir(), "flush-failure")
	if errSource != nil {
		t.Fatal(errSource)
	}
	t.Cleanup(func() { _ = source.Cleanup() })
	if errAppend := source.AppendBytes([]byte("pending bytes")); errAppend != nil {
		t.Fatal(errAppend)
	}
	if errClose := source.appendFile.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	if _, errRead := source.Bytes(); errRead == nil {
		t.Fatal("a failed flush was reported as a successful read")
	}
}

func BenchmarkLogSpool(b *testing.B) {
	for _, events := range []bool{false, true} {
		kind := "SSE"
		if events {
			kind = "WebSocket"
		}
		for _, legacy := range []bool{true, false} {
			mode := "buffered"
			if legacy {
				mode = "legacy"
			}
			b.Run(kind+"/"+mode, func(b *testing.B) {
				dir := b.TempDir()
				payload := bytes.Repeat([]byte("data: response delta\n"), 10)
				b.ReportAllocs()
				b.SetBytes(int64(1000 * len(payload)))
				for i := 0; i < b.N; i++ {
					source, errSource := NewFileBodySourceInDir(dir, "bench")
					if errSource != nil {
						b.Fatal(errSource)
					}
					for j := 0; j < 1000; j++ {
						var errAppend error
						if legacy {
							errAppend = legacySpoolAppend(source, payload, events)
						} else if events {
							errAppend = source.AppendPart(payload)
						} else {
							errAppend = source.AppendBytes(payload)
						}
						if errAppend != nil {
							b.Fatal(errAppend)
						}
					}
					if errCleanup := source.Cleanup(); errCleanup != nil {
						b.Fatal(errCleanup)
					}
				}
			})
		}
	}
}

// Reproduce the previous per-append filesystem operations for comparison.
func legacySpoolAppend(source *FileBodySource, data []byte, event bool) error {
	if event {
		part, errPart := source.CreatePart("part")
		if errPart != nil {
			return errPart
		}
		errWrite := writeLogPart(part, bytes.TrimSpace(data), false)
		errClose := part.Close()
		if errWrite != nil {
			return errWrite
		}
		return errClose
	}
	if errMkdir := os.MkdirAll(source.dir, 0755); errMkdir != nil {
		return errMkdir
	}
	var file *os.File
	var errOpen error
	if len(source.paths) == 0 {
		file, errOpen = os.CreateTemp(source.dir, "part-*.tmp")
		if errOpen == nil {
			source.paths = append(source.paths, filepath.Clean(file.Name()))
		}
	} else {
		file, errOpen = os.OpenFile(source.paths[len(source.paths)-1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}
	if errOpen != nil {
		return errOpen
	}
	_, errWrite := file.Write(data)
	errClose := file.Close()
	if errWrite != nil {
		return errWrite
	}
	return errClose
}
