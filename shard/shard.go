// Package shard defines the shard interface and an injectable fake shard.
package shard

import (
	"context"
	"time"
)

// Record is one item returned by a shard. Missing fields keep zero values.
type Record struct {
	ID string
	V  float64
}

// Shard is the injectable data source. Results stream chunk by chunk so that
// timeouts, duplicated chunks and corruption can be simulated.
type Shard interface {
	// ID returns the shard identifier; the empty string is legal.
	ID() string
	// Bound returns the largest score any record of this shard could have.
	// It must be available even when Query fails.
	Bound() float64
	// Claimed returns the number of records the shard says it delivers.
	Claimed() int
	// Query streams result chunks onto out and closes it when finished.
	Query(ctx context.Context, out chan<- []Record) error
}

// Config configures a Fake shard.
type Config struct {
	ShardID   string
	Records   []Record
	Delay     time.Duration // delay before the first chunk
	Hang      bool          // never send and never return until ctx is done
	Fail      bool          // return an error immediately after delay
	Corrupt   bool          // claim a different count than delivered
	Duplicate bool          // send the chunk a second time (retry overlap)
	Chunk     int           // records per chunk; <=1 means one record per chunk
}

// New builds a fake shard. The bound defaults to the max record score and can
// be overridden with SetBound (used to build TopK prefix scenarios).
func New(c Config) *Fake {
	bound := 0.0
	for _, r := range c.Records {
		if r.V > bound {
			bound = r.V
		}
	}
	return &Fake{cfg: c, bound: bound}
}

// Fake is the controllable shard implementation.
type Fake struct {
	cfg   Config
	bound float64
}

// SetBound overrides the shard upper bound.
func (f *Fake) SetBound(v float64) { f.bound = v }

// ID returns the configured shard id.
func (f *Fake) ID() string { return f.cfg.ShardID }

// Bound returns the configured upper bound.
func (f *Fake) Bound() float64 { return f.bound }

// Claimed returns the record count the shard reports. A corrupt shard claims
// one more record than it actually delivers.
func (f *Fake) Claimed() int {
	if f.cfg.Corrupt {
		return len(f.cfg.Records) + 1
	}
	return len(f.cfg.Records)
}

// Query implements Shard.
func (f *Fake) Query(ctx context.Context, out chan<- []Record) error {
	defer close(out)
	if f.cfg.Hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.cfg.Delay > 0 {
		select {
		case <-time.After(f.cfg.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.cfg.Fail {
		return errShardFailed
	}

	chunks := split(f.cfg.Records, f.cfg.Chunk)
	for i, ch := range chunks {
		if !send(ctx, out, ch) {
			return ctx.Err()
		}
		if f.cfg.Duplicate && i == 0 && !send(ctx, out, ch) {
			return ctx.Err()
		}
	}
	return nil
}

func split(rs []Record, size int) [][]Record {
	if size <= 1 {
		size = 1
	}
	var out [][]Record
	for i := 0; i < len(rs); i += size {
		end := i + size
		if end > len(rs) {
			end = len(rs)
		}
		out = append(out, rs[i:end])
	}
	return out
}

func send(ctx context.Context, out chan<- []Record, ch []Record) bool {
	select {
	case out <- ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// ErrShardFailed is returned by failing fake shards.
var ErrShardFailed = errShardFailed

type shardError struct{ msg string }

func (e *shardError) Error() string { return e.msg }

var errShardFailed = &shardError{msg: "shard: injected failure"}
