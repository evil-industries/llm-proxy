package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionCache_CapacityEvictionOrder(t *testing.T) {
	t.Parallel()

	maxEntries := 5
	cache := NewSessionCacheWithCapacity(time.Hour, maxEntries)
	defer cache.Stop()

	// Insert 5 entries (fill cache to capacity)
	for i := 1; i <= 5; i++ {
		cache.Set(fmt.Sprintf("sess-%d", i), fmt.Sprintf("auth-%d", i))
	}

	if cache.Len() != 5 {
		t.Fatalf("cache.Len() = %d, want 5", cache.Len())
	}

	// Insert 6th entry: oldest (sess-1) should be evicted
	cache.Set("sess-6", "auth-6")

	if cache.Len() > maxEntries {
		t.Fatalf("cache.Len() = %d, want <= %d", cache.Len(), maxEntries)
	}

	if _, ok := cache.Get("sess-1"); ok {
		t.Fatal("expected oldest entry sess-1 to be evicted")
	}

	for i := 2; i <= 6; i++ {
		if got, ok := cache.Get(fmt.Sprintf("sess-%d", i)); !ok || got != fmt.Sprintf("auth-%d", i) {
			t.Fatalf("Get(sess-%d) = %q, %v; want auth-%d, true", i, got, ok, i)
		}
	}
}

func TestSessionCache_MultiAliasGroupEviction(t *testing.T) {
	t.Parallel()

	maxEntries := 4
	cache := NewSessionCacheWithCapacity(time.Hour, maxEntries)
	defer cache.Stop()

	// Group 1: 2 aliases (s1-a, s1-b)
	cache.SetAliases("auth-1", "s1-a", "s1-b")
	// Group 2: 2 aliases (s2-a, s2-b)
	cache.SetAliases("auth-2", "s2-a", "s2-b")

	if cache.Len() != 4 {
		t.Fatalf("cache.Len() = %d, want 4", cache.Len())
	}

	// Group 3: 1 alias (s3)
	// Inserting s3 should evict Group 1 completely (both s1-a and s1-b)
	cache.Set("s3", "auth-3")

	if cache.Len() > maxEntries {
		t.Fatalf("cache.Len() = %d, want <= %d", cache.Len(), maxEntries)
	}

	if _, ok := cache.Get("s1-a"); ok {
		t.Fatal("expected s1-a to be evicted")
	}
	if _, ok := cache.Get("s1-b"); ok {
		t.Fatal("expected s1-b to be evicted")
	}

	if got, ok := cache.Get("s2-a"); !ok || got != "auth-2" {
		t.Fatalf("Get(s2-a) = %q, %v", got, ok)
	}
	if got, ok := cache.Get("s3"); !ok || got != "auth-3" {
		t.Fatalf("Get(s3) = %q, %v", got, ok)
	}
}

func TestSessionCache_HighThroughputCapacitySaturated(t *testing.T) {
	t.Parallel()

	maxEntries := 1000
	cache := NewSessionCacheWithCapacity(time.Hour, maxEntries)
	defer cache.Stop()

	// Rapidly write 10,000 entries through a small capacity cache to ensure O(1) eviction
	for i := 0; i < 10000; i++ {
		cache.Set(fmt.Sprintf("sess-%d", i), fmt.Sprintf("auth-%d", i%10))
	}

	if cache.Len() > maxEntries {
		t.Fatalf("cache.Len() = %d, want <= %d", cache.Len(), maxEntries)
	}

	// Latest entries must be present
	for i := 9900; i < 10000; i++ {
		if got, ok := cache.Get(fmt.Sprintf("sess-%d", i)); !ok || got != fmt.Sprintf("auth-%d", i%10) {
			t.Fatalf("Get(sess-%d) = %q, %v", i, got, ok)
		}
	}
}

func TestSessionCache_ConcurrentSaturatedAccess(t *testing.T) {
	t.Parallel()

	maxEntries := 50
	cache := NewSessionCacheWithCapacity(time.Hour, maxEntries)
	defer cache.Stop()

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := fmt.Sprintf("worker-%d-sess-%d", w, i)
				cache.Set(key, fmt.Sprintf("auth-%d", w))
				cache.Get(key)
				if i%5 == 0 {
					cache.Touch(key, fmt.Sprintf("auth-%d", w))
				}
				if i%7 == 0 {
					cache.CompareAndDelete(key, fmt.Sprintf("auth-%d", w))
				}
			}
		}(worker)
	}
	wg.Wait()

	if cache.Len() > maxEntries {
		t.Fatalf("cache.Len() = %d, want <= %d", cache.Len(), maxEntries)
	}
}

func TestSessionCache_NilReceiverSafety(t *testing.T) {
	t.Parallel()

	var cache *SessionCache
	if got, ok := cache.Get("session-1"); ok || got != "" {
		t.Fatalf("nil.Get() = %q, %v; want '', false", got, ok)
	}
	if got, ok := cache.GetAndRefresh("session-1"); ok || got != "" {
		t.Fatalf("nil.GetAndRefresh() = %q, %v; want '', false", got, ok)
	}
	cache.Set("session-1", "auth-1")
	cache.SetAliases("auth-1", "s1", "s2")
	if ok := cache.Touch("session-1", "auth-1"); ok {
		t.Fatal("nil.Touch() unexpectedly succeeded")
	}
	if ok := cache.CompareAndDelete("session-1", "auth-1"); ok {
		t.Fatal("nil.CompareAndDelete() unexpectedly succeeded")
	}
	cache.Invalidate("session-1")
	cache.InvalidateAuth("auth-1")
	if n := cache.Len(); n != 0 {
		t.Fatalf("nil.Len() = %d, want 0", n)
	}
	cache.Stop()
}

func TestSessionCache_StopNilChannelNoPanic(t *testing.T) {
	t.Parallel()

	zeroCache := &SessionCache{}
	zeroCache.Stop()
}

func TestSessionCache_InactivityBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		beyond time.Duration
		valid  bool
	}{
		{name: "at lifetime", valid: true},
		{name: "past lifetime", beyond: time.Nanosecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := time.Unix(1800000000, 0)
			cache := &SessionCache{ttl: time.Hour, nowFunc: func() time.Time { return current }}
			cache.SetAliases("auth-1", "session-1", "alias-1")
			current = current.Add(cache.ttl + test.beyond)
			cache.cleanup()
			for _, alias := range []string{"session-1", "alias-1"} {
				authID, ok := cache.Get(alias)
				if ok != test.valid || (ok && authID != "auth-1") {
					t.Fatalf("Get(%q) = %q, %v, want valid=%v", alias, authID, ok, test.valid)
				}
			}
			if !test.valid && cache.Len() != 0 {
				t.Fatal("expired session aliases remained in the cache")
			}
		})
	}
}

func TestSessionCache_InactivityRefreshAppliesToAllAliases(t *testing.T) {
	t.Parallel()

	current := time.Unix(1800000000, 0)
	cache := &SessionCache{ttl: time.Hour, nowFunc: func() time.Time { return current }}
	cache.SetAliases("auth-1", "session-1", "alias-1")

	current = current.Add(cache.ttl)
	if got, ok := cache.GetAndRefresh("alias-1"); !ok || got != "auth-1" {
		t.Fatalf("GetAndRefresh() at lifetime = %q, %v, want auth-1, true", got, ok)
	}
	current = current.Add(cache.ttl)
	if !cache.Touch("session-1", "auth-1") {
		t.Fatal("Touch() did not refresh the session through its other alias")
	}
	current = current.Add(cache.ttl)
	if got, ok := cache.Get("alias-1"); !ok || got != "auth-1" {
		t.Fatalf("Get() after alias refresh = %q, %v, want auth-1, true", got, ok)
	}

	// An observation does not count as another message or extend affinity.
	current = current.Add(time.Nanosecond)
	if got, ok := cache.GetAndRefresh("session-1"); ok || got != "" {
		t.Fatalf("GetAndRefresh() after inactivity = %q, %v, want empty, false", got, ok)
	}
	if cache.Len() != 0 {
		t.Fatal("expired session left an alias that could restore affinity")
	}
}

func TestSessionCache_SetAliasesAtInactivityBoundary(t *testing.T) {
	t.Parallel()

	current := time.Unix(1800000000, 0)
	cache := &SessionCache{ttl: time.Hour, nowFunc: func() time.Time { return current }}
	cache.SetAliases("auth-1", "session-1", "alias-1")
	current = current.Add(cache.ttl)
	cache.SetAliases("auth-1", "session-1", "alias-2")
	current = current.Add(cache.ttl)
	for _, alias := range []string{"session-1", "alias-1", "alias-2"} {
		if got, ok := cache.Get(alias); !ok || got != "auth-1" {
			t.Fatalf("Get(%q) = %q, %v, want auth-1, true", alias, got, ok)
		}
	}
}
