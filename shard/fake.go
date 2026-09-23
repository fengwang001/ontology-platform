package shard

import (
	"context"
	"time"
)

// Shard is the injectable interface modeled by Fake. Fetch may invoke
// deliver zero or more times (multiple times simulates retried/duplicate
// delivery) and must return promptly when ctx is canceled.
type Shard interface {
	ID() string
	Fetch(ctx context.Context, deliver func(Frame)) error
}

// Fake is a scripted in-process shard.
type Fake struct {
	id        string
	records   []Record
	maxScore  int64
	delay     time.Duration
	hang      bool
	corrupt   bool
	repeats   int
	repeatGap time.Duration
	fail      error
}

// Option configures a Fake at construction.
type Option func(*Fake)

// NewFake builds a fake shard that serves records once by default.
func NewFake(id string, records []Record, maxScore int64, opts ...Option) *Fake {
	f := &Fake{id: id, records: records, maxScore: maxScore, repeats: 1}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// WithDelay makes the shard wait d before delivering (ctx-aware).
func WithDelay(d time.Duration) Option { return func(f *Fake) { f.delay = d } }

// WithHang makes the shard never deliver until ctx is canceled.
func WithHang() Option { return func(f *Fake) { f.hang = true } }

// WithCorrupt makes the shard claim a count that mismatches its payload.
func WithCorrupt() Option { return func(f *Fake) { f.corrupt = true } }

// WithRepeats delivers the same frame n times (n >= 2 simulates retry dup).
func WithRepeats(n int) Option { return func(f *Fake) { f.repeats = n } }

// WithRepeatGap sets the pause between repeated deliveries.
func WithRepeatGap(d time.Duration) Option { return func(f *Fake) { f.repeatGap = d } }

// WithFail makes Fetch fail with an explicit error instead of delivering.
func WithFail(err error) Option { return func(f *Fake) { f.fail = err } }

// ID returns the shard id; the empty string is a legal id.
func (f *Fake) ID() string { return f.id }

// Fetch implements Shard.
func (f *Fake) Fetch(ctx context.Context, deliver func(Frame)) error {
	if f.fail != nil {
		return f.fail
	}
	if f.hang {
		<-ctx.Done()
		return ErrTimeout
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ErrTimeout
		}
	}
	claimed := len(f.records)
	if f.corrupt {
		claimed++
	}
	n := f.repeats
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return ErrTimeout
		default:
		}
		deliver(Frame{ShardID: f.id, Claimed: claimed, Records: f.records, MaxScore: f.maxScore})
		if i+1 < n && f.repeatGap > 0 {
			select {
			case <-time.After(f.repeatGap):
			case <-ctx.Done():
				return ErrTimeout
			}
		}
	}
	return nil
}

// I64 is a convenience for building non-nil int64 pointer fields.
func I64(v int64) *int64 { return &v }
