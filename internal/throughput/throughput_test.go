package throughput

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestRollingMetrics(t *testing.T) {
	now := time.Unix(1800000000, 0)
	c := NewCollector(now.Add(-10 * time.Minute))
	c.record(coreusage.Record{Stream: true, Latency: 5 * time.Second, TTFT: time.Second, Detail: coreusage.Detail{OutputTokens: 200}}, now.Add(-time.Second))
	c.record(coreusage.Record{Failed: true, Latency: time.Second, Detail: coreusage.Detail{OutputTokens: 999}}, now)
	// An expired record and a future record must not affect current rates.
	c.record(coreusage.Record{Detail: coreusage.Detail{OutputTokens: 10000}}, now.Add(-301*time.Second))
	c.record(coreusage.Record{}, now.Add(time.Second))
	got := c.Snapshot(now)
	if got.Requests != 2 || got.Failed != 1 || got.OutputTokens != 200 || got.RequestsPerMinute != 0.4 || got.OutputTokensPerSecond != 200.0/300 {
		t.Fatalf("unexpected rates: %+v", got)
	}
	if got.GenerationTokensPerSecond == nil || *got.GenerationTokensPerSecond != 50 || got.AverageLatencyMS == nil || *got.AverageLatencyMS != 3000 || got.AverageTTFTMS == nil || *got.AverageTTFTMS != 1000 {
		t.Fatalf("unexpected timing: %+v", got)
	}
	expired := c.Snapshot(now.Add(302 * time.Second))
	if expired.Requests != 0 || expired.OutputTokens != 0 || expired.GenerationTokensPerSecond != nil || expired.AverageLatencyMS != nil || expired.AverageTTFTMS != nil {
		t.Fatalf("expired metrics retained: %+v", expired)
	}
}

func TestMissingTimingAndWarmup(t *testing.T) {
	now := time.Unix(1800000000, 0)
	c := NewCollector(now.Add(-10 * time.Second))
	c.record(coreusage.Record{Latency: time.Second, Detail: coreusage.Detail{OutputTokens: 100}}, now)
	c.record(coreusage.Record{Stream: true, Latency: time.Second, TTFT: 2 * time.Second}, now)
	got := c.Snapshot(now)
	if got.WindowSeconds != 10 || got.OutputTokensPerSecond != 10 || got.GenerationTokensPerSecond != nil || got.AverageTTFTMS != nil {
		t.Fatalf("missing timing inferred or incorrect warmup: %+v", got)
	}
	empty := NewCollector(now).Snapshot(now)
	if empty.WindowSeconds != 1 || empty.RequestsPerMinute != 0 || empty.AverageLatencyMS != nil {
		t.Fatalf("invalid empty snapshot: %+v", empty)
	}
}

func TestBucketReuseAndNoGeneration(t *testing.T) {
	now := time.Unix(1800000000, 0)
	c := NewCollector(now)
	c.record(coreusage.Record{Detail: coreusage.Detail{OutputTokens: 500}}, now)
	c.record(coreusage.Record{Generate: coreusage.GenerateFlag(false), Stream: true, Latency: 2 * time.Second, TTFT: time.Second, Detail: coreusage.Detail{OutputTokens: 999}}, now.Add(300*time.Second))
	got := c.Snapshot(now.Add(300 * time.Second))
	if got.Requests != 1 || got.OutputTokens != 0 || got.GenerationTokensPerSecond != nil || got.AverageTTFTMS != nil {
		t.Fatalf("incorrect reused bucket: %+v", got)
	}
}

func TestCollectionHonorsStatisticsToggle(t *testing.T) {
	previous := redisqueue.UsageStatisticsEnabled()
	t.Cleanup(func() { redisqueue.SetUsageStatisticsEnabled(previous) })
	c := NewCollector(time.Now())
	redisqueue.SetUsageStatisticsEnabled(false)
	c.HandleUsage(context.Background(), coreusage.Record{Detail: coreusage.Detail{OutputTokens: 100}})
	if got := c.Snapshot(time.Now()); got.Requests != 0 {
		t.Fatalf("disabled collection: %+v", got)
	}
	redisqueue.SetUsageStatisticsEnabled(true)
	c.HandleUsage(context.Background(), coreusage.Record{Detail: coreusage.Detail{OutputTokens: 100}})
	if got := c.Snapshot(time.Now()); got.Requests != 1 || got.OutputTokens != 100 {
		t.Fatalf("enabled collection: %+v", got)
	}
}
