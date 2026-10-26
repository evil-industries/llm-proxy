// Package throughput keeps bounded, anonymous runtime aggregates for management.
package throughput

import (
	"context"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const windowSeconds = 300

type totals struct {
	requests    int64
	failed      int64
	output      int64
	latency     time.Duration
	timed       int64
	ttft        time.Duration
	firstTokens int64
	generation  time.Duration
	generated   int64
}

type bucket struct {
	second int64
	totals
}

// Collector retains five minutes of completed upstream attempts without identifiers.
type Collector struct {
	mu      sync.Mutex
	started time.Time
	buckets [windowSeconds]bucket
}

// Snapshot describes completed upstream attempts in a rolling window. Timing
// averages are nil until a provider reports a usable measurement.
type Snapshot struct {
	WindowSeconds             float64  `json:"window_seconds"`
	Requests                  int64    `json:"requests"`
	Failed                    int64    `json:"failed"`
	OutputTokens              int64    `json:"output_tokens"`
	RequestsPerMinute         float64  `json:"requests_per_minute"`
	OutputTokensPerSecond     float64  `json:"output_tokens_per_second"`
	GenerationTokensPerSecond *float64 `json:"generation_tokens_per_second"`
	AverageLatencyMS          *float64 `json:"average_latency_ms"`
	AverageTTFTMS             *float64 `json:"average_ttft_ms"`
}

var Default = NewCollector(time.Now())

func init() { coreusage.RegisterPlugin(Default) }

func NewCollector(now time.Time) *Collector { return &Collector{started: now} }

func (c *Collector) HandleUsage(_ context.Context, record coreusage.Record) {
	if !redisqueue.UsageStatisticsEnabled() {
		return
	}
	c.record(record, time.Now())
}

func (c *Collector) record(record coreusage.Record, completed time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	second := completed.Unix()
	index := (second%windowSeconds + windowSeconds) % windowSeconds
	b := &c.buckets[index]
	if b.second > second {
		return
	}
	if b.second != second {
		*b = bucket{second: second}
	}
	b.requests++
	if record.Failed {
		b.failed++
	}
	if record.Latency > 0 {
		b.latency += record.Latency
		b.timed++
	}
	if record.Failed || !coreusage.GenerateEnabled(record.Generate) {
		return
	}
	// OutputTokens already includes reasoning where the provider reports it.
	output := record.Detail.OutputTokens
	if output > 0 {
		b.output += output
	}
	if record.TTFT > 0 && record.TTFT <= record.Latency {
		b.ttft += record.TTFT
		b.firstTokens++
		// Streaming generation time excludes the wait for the first token. Do not
		// infer generation speed from non-streaming responses or missing TTFT.
		if record.Stream && output > 0 && record.Latency > record.TTFT {
			b.generation += record.Latency - record.TTFT
			b.generated += output
		}
	}
}

func (c *Collector) Snapshot(now time.Time) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	seconds := min(float64(windowSeconds), max(1, now.Sub(c.started).Seconds()))
	var sum totals
	for _, b := range c.buckets {
		if b.second <= now.Unix()-windowSeconds || b.second > now.Unix() {
			continue
		}
		sum.requests += b.requests
		sum.failed += b.failed
		sum.output += b.output
		sum.latency += b.latency
		sum.timed += b.timed
		sum.ttft += b.ttft
		sum.firstTokens += b.firstTokens
		sum.generation += b.generation
		sum.generated += b.generated
	}
	result := Snapshot{
		WindowSeconds:         seconds,
		Requests:              sum.requests,
		Failed:                sum.failed,
		OutputTokens:          sum.output,
		RequestsPerMinute:     float64(sum.requests) * 60 / seconds,
		OutputTokensPerSecond: float64(sum.output) / seconds,
	}
	if sum.timed > 0 {
		value := float64(sum.latency) / float64(time.Millisecond) / float64(sum.timed)
		result.AverageLatencyMS = &value
	}
	if sum.firstTokens > 0 {
		value := float64(sum.ttft) / float64(time.Millisecond) / float64(sum.firstTokens)
		result.AverageTTFTMS = &value
	}
	if sum.generation > 0 {
		value := float64(sum.generated) / sum.generation.Seconds()
		result.GenerationTokensPerSecond = &value
	}
	return result
}
