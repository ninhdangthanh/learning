package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func allowN(t *testing.T, limiter Limiter, key string, n int) []Result {
	t.Helper()
	results := make([]Result, 0, n)
	for i := 0; i < n; i++ {
		result, err := limiter.Allow(context.Background(), key)
		if err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
		results = append(results, result)
	}
	return results
}

func TestFixedWindowBlocksBeyondLimit(t *testing.T) {
	limiter := NewFixedWindow(newTestClient(t), "test", FixedWindowConfig{Limit: 3, Window: time.Minute})

	results := allowN(t, limiter, "user-1", 5)

	for i := 0; i < 3; i++ {
		if !results[i].Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	for i := 3; i < 5; i++ {
		if results[i].Allowed {
			t.Fatalf("request %d should be blocked", i)
		}
		if results[i].RetryAfter <= 0 {
			t.Fatalf("blocked request %d should carry retry-after", i)
		}
	}
}

func TestFixedWindowIsolatesKeys(t *testing.T) {
	limiter := NewFixedWindow(newTestClient(t), "test", FixedWindowConfig{Limit: 1, Window: time.Minute})

	first := allowN(t, limiter, "user-1", 2)
	second := allowN(t, limiter, "user-2", 1)

	if first[1].Allowed {
		t.Fatal("second request for user-1 should be blocked")
	}
	if !second[0].Allowed {
		t.Fatal("first request for user-2 should be allowed")
	}
}

func TestSlidingWindowBlocksBeyondLimit(t *testing.T) {
	limiter := NewSlidingWindow(newTestClient(t), "test", SlidingWindowConfig{Limit: 2, Window: time.Minute})

	results := allowN(t, limiter, "user-1", 4)

	if !results[0].Allowed || !results[1].Allowed {
		t.Fatal("first two requests should be allowed")
	}
	if results[2].Allowed || results[3].Allowed {
		t.Fatal("requests beyond the limit should be blocked")
	}
	if results[1].Remaining != 0 {
		t.Fatalf("remaining should be 0 after the limit is reached, got %d", results[1].Remaining)
	}
}

func TestTokenBucketAllowsBurstThenBlocks(t *testing.T) {
	limiter := NewTokenBucket(newTestClient(t), "test", TokenBucketConfig{Capacity: 3, RefillPerSecond: 1})

	results := allowN(t, limiter, "user-1", 4)

	for i := 0; i < 3; i++ {
		if !results[i].Allowed {
			t.Fatalf("burst request %d should be allowed", i)
		}
	}
	if results[3].Allowed {
		t.Fatal("request beyond the bucket capacity should be blocked")
	}
	if results[3].RetryAfter <= 0 {
		t.Fatal("blocked request should carry retry-after")
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	client := newTestClient(t)
	limiter := NewTokenBucket(client, "test", TokenBucketConfig{Capacity: 2, RefillPerSecond: 100})

	allowN(t, limiter, "user-1", 2)

	blocked, err := limiter.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if blocked.Allowed {
		t.Fatal("bucket should be empty")
	}

	time.Sleep(50 * time.Millisecond)

	refilled, err := limiter.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("allow after refill: %v", err)
	}
	if !refilled.Allowed {
		t.Fatal("bucket should have refilled")
	}
}
