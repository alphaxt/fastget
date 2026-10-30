package engine

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is a thread-safe token bucket that caps download throughput.
// One limiter can be shared by many downloads (the queue does this), so the
// configured rate is a cap on the total of all of them. The rate can be changed
// at any time with SetRate and takes effect immediately.
//
// Tokens represent bytes. The bucket refills at the rate in bytes/sec up to a
// burst ceiling. A reader asks for N tokens before accepting N bytes it read;
// if not enough are available it waits for the bucket to refill.
type RateLimiter struct {
	mu       sync.Mutex
	rate     float64 // bytes per second; 0 = unlimited
	burst    float64
	tokens   float64
	lastFill time.Time
}

// NewRateLimiter builds a limiter for bytesPerSec (0 = unlimited).
func NewRateLimiter(bytesPerSec int64) *RateLimiter {
	l := &RateLimiter{lastFill: time.Now()}
	l.SetRate(bytesPerSec)
	return l
}

// SetRate changes the limit (0 = unlimited).
func (l *RateLimiter) SetRate(bytesPerSec int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if bytesPerSec <= 0 {
		l.rate = 0
		return
	}
	l.refillLocked(time.Now())
	l.rate = float64(bytesPerSec)
	// About 0.5 s of burst keeps throughput smooth, but at least one 64 KB read.
	l.burst = l.rate * 0.5
	if l.burst < 64*1024 {
		l.burst = 64 * 1024
	}
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
}

// newRateLimiter returns nil for "unlimited" so a per-download limiter costs
// nothing when no limit is set.
func newRateLimiter(bytesPerSec int64) *RateLimiter {
	if bytesPerSec <= 0 {
		return nil
	}
	l := NewRateLimiter(bytesPerSec)
	l.tokens = l.burst // start full so the first reads are not delayed
	return l
}

func (l *RateLimiter) refillLocked(now time.Time) {
	elapsed := now.Sub(l.lastFill).Seconds()
	l.lastFill = now
	if elapsed <= 0 || l.rate == 0 {
		return
	}
	l.tokens += elapsed * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
}

// wait blocks until n byte-tokens are available (or ctx is cancelled), then
// consumes them. A nil or unlimited limiter returns immediately.
func (l *RateLimiter) wait(ctx context.Context, n int) error {
	if l == nil || n <= 0 {
		return nil
	}
	need := float64(n)
	for {
		l.mu.Lock()
		l.refillLocked(time.Now())
		if l.rate == 0 {
			l.mu.Unlock()
			return nil
		}
		if l.tokens >= need {
			l.tokens -= need
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration((need - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()

		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond // re-check ctx and rate changes promptly
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
