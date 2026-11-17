package logarchive

import "testing"

// Pins completed-log name matching, including anchoring and non-ASCII input.
func TestIsCompletedLogNamePinnedCases(t *testing.T) {
	for name, want := range map[string]bool{
		"v1-responses-2026-10-27T120000-abcd1234.log":    true,
		"v1-responses-2026-10-27T120000-abcd1234.log.gz": true,
		"error-v1-chat-2026-10-27T120000-x.log":          true,
		"recovered-anything.log":                         true,
		"recovered-anything.log.gz":                      true,
		"ünïcode-2026-10-27T120000-ß.log":                true,
		"v1-2026-10-27T120000-a.log.gz.gz":               false,
		"v1-2026-10-27T12000-a.log":                      false,
		"v1-2026-10-27T120000-.log":                      false,
		"v1-2026-10-27T120000-a.txt":                     false,
		"xv1-2026-10-27T120000-a.logx":                   false,
		"recovered-.log":                                 false,
		"recovered.log":                                  false,
		"v1-٢٠٢٦-10-27T120000-a.log":                     false,
		"main.log":                                       false,
		"main.log.1":                                     true,
		"":                                               false,
	} {
		if got := IsCompletedLogName(name); got != want {
			t.Errorf("IsCompletedLogName(%q) = %v, want %v", name, got, want)
		}
	}
}
