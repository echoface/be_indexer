package segmentindex

import (
	"context"
	"sync"
	"time"
)

// RateLimiter throttles background work (compaction merges) so it cannot
// starve online queries. n is an abstract cost unit (merged doc versions).
type RateLimiter interface {
	WaitN(ctx context.Context, n int) error
}

// TokenBucket is a simple token bucket rate limiter. A rate <= 0 disables
// limiting. Requests larger than the burst are still served after a
// proportional wait instead of being rejected.
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second; <= 0 means unlimited
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewTokenBucket returns a bucket refilling at rate tokens per second with the
// given burst. rate <= 0 returns an unlimited limiter.
func NewTokenBucket(rate, burst float64) *TokenBucket {
	if burst <= 0 {
		burst = rate
	}
	if burst <= 0 {
		burst = 1
	}
	return &TokenBucket{rate: rate, burst: burst, tokens: burst, last: time.Now(), now: time.Now}
}

// WaitN blocks until n tokens are available or ctx is done.
func (b *TokenBucket) WaitN(ctx context.Context, n int) error {
	if b == nil || b.rate <= 0 || n <= 0 {
		return ctx.Err()
	}
	need := float64(n)
	for {
		b.mu.Lock()
		now := b.now()
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens += elapsed * b.rate
			if b.tokens > b.burst {
				b.tokens = b.burst
			}
			b.last = now
		}
		if b.tokens >= need {
			b.tokens -= need
			b.mu.Unlock()
			return ctx.Err()
		}
		missing := need - b.tokens
		// spend what is there now and wait out the rest
		b.tokens = 0
		wait := time.Duration(missing / b.rate * float64(time.Second))
		b.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// unlimitedLimiter never blocks.
type unlimitedLimiter struct{}

func (unlimitedLimiter) WaitN(context.Context, int) error { return nil }

// UnlimitedLimiter returns a RateLimiter that never throttles.
func UnlimitedLimiter() RateLimiter { return unlimitedLimiter{} }
