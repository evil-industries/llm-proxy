package logarchive

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var completedRequestPattern = regexp.MustCompile(`^(.+-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{6}-.+|recovered-.+)\.log(\.gz)?$`)

func safeLogBasename(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// ApplicationRotationOrder recognizes legacy numeric and timestamp application
// rotations, with optional gzip. Its ordering matches management log pagination.
func ApplicationRotationOrder(name string) (int64, bool) {
	if order, ok := NumericRotationOrder(name); ok {
		return order, true
	}
	return TimestampRotationOrder(name)
}

// NumericRotationOrder preserves the historical integer suffix ordering.
func NumericRotationOrder(name string) (int64, bool) {
	if !safeLogBasename(name) {
		return 0, false
	}
	name = strings.TrimSuffix(name, ".gz")
	if !strings.HasPrefix(name, "main.log.") {
		return 0, false
	}
	order, err := strconv.Atoi(strings.TrimPrefix(name, "main.log."))
	return int64(order), err == nil
}

// TimestampRotationOrder accepts second and subsecond lumberjack rotations.
// Historical management readers ignored the fraction, including legacy suffixes;
// keep that behavior so archival and management agree on existing files.
func TimestampRotationOrder(name string) (int64, bool) {
	if !safeLogBasename(name) {
		return 0, false
	}
	name = strings.TrimSuffix(name, ".gz")
	if !strings.HasPrefix(name, "main-") || !strings.HasSuffix(name, ".log") {
		return 0, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "main-"), ".log")
	if dot := strings.IndexByte(stamp, '.'); dot != -1 {
		stamp = stamp[:dot]
	}
	timestamp, err := time.ParseInLocation("2006-01-02T15-04-05", stamp, time.Local)
	if err != nil {
		return 0, false
	}
	return math.MaxInt64 - timestamp.Unix(), true
}

// IsCompletedLogName recognizes immutable files emitted by current and legacy
// log writers. Active main.log, temporary files, and path components are excluded.
func IsCompletedLogName(name string) bool {
	if !safeLogBasename(name) {
		return false
	}
	if _, ok := ApplicationRotationOrder(name); ok {
		return true
	}
	return completedRequestPattern.MatchString(name)
}
