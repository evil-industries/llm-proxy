package logarchive

import "testing"

func TestApplicationRotationNames(t *testing.T) {
	for _, name := range []string{
		"main.log.1", "main.log.1.gz", "main.log.0", "main.log.01", "main.log.-1", "main.log.+1.gz",
		"main-2026-06-15T10-00-00.log", "main-2026-06-15T10-00-00.log.gz",
		"main-2026-06-15T10-00-00.123.log", "main-2026-06-15T10-00-00.123.log.gz",
		"main-2026-06-15T10-00-00.legacy.log",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ApplicationRotationOrder(name); !ok {
				t.Fatal("legacy rotation not recognized")
			}
			if !IsCompletedLogName(name) {
				t.Fatal("legacy rotation is not eligible for archival")
			}
		})
	}
	for _, name := range []string{
		"main.log", "main.log.gz", "main.log.", "main.log.one", "main.log.1.tmp", "main.log.1.gz.gz",
		"main.log.9999999999999999999999999", "main-2026-99-15T10-00-00.log", "main-2026-06-15T25-00-00.log",
		"main-2026-06-15T10-00-00", "main-2026-06-15T10-00-00.log.pending", "main-2026-06-15T10-00-00.gz",
		"../main.log.1", "/main.log.1", `dir\main.log.1`, "main-2026-06-15T10-00-00.\n.log",
		"main-2026-06-15T10-00-00.\x00.log", "main-2026-06-15T10-00-00.\x7f.log",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ApplicationRotationOrder(name); ok {
				t.Fatal("unsafe or active file recognized as rotation")
			}
			if IsCompletedLogName(name) {
				t.Fatal("unsafe or active file eligible for archival")
			}
		})
	}
}

func TestApplicationRotationOrderingCompatibility(t *testing.T) {
	for _, pair := range [][2]string{
		{"main.log.1", "main.log.2"},
		{"main-2026-06-15T10-00-01.log", "main-2026-06-15T10-00-00.log"},
	} {
		first, okFirst := ApplicationRotationOrder(pair[0])
		second, okSecond := ApplicationRotationOrder(pair[1])
		if !okFirst || !okSecond || first >= second {
			t.Fatalf("unexpected newest-first ordering: %v -> %d, %d", pair, first, second)
		}
	}
	plain, _ := ApplicationRotationOrder("main-2026-06-15T10-00-00.log")
	fractional, _ := ApplicationRotationOrder("main-2026-06-15T10-00-00.123.log.gz")
	if plain != fractional {
		t.Fatal("historical subsecond ordering changed")
	}
}
