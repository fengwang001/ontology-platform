// Package shard defines the shard interface and an injectable fake shard.
package shard

import (
	"context"
	"time"
)

// Record is a single scored item returned by a shard. Value must be >= 0.
type Record struct {
	ID    string
	Value float64
}

// Result is what a shard returns for one query. Bound is the shard's
// declared upper bound on any Value it could ever hold; it is set even
// on failure so confidence bounds can still be derived.
type Result struct {
	ShardID string
	Claimed int // record count the shard claims to return
	Records []Record
	Bound   float64
	Err     error
}

// Shard is the injectable query endpoint. No real network is involved.
type Shard interface {
	ID() string
	UpperBound() float64
	Query(ctx context.Context) Result
}

// Fake is a configurable Shard for tests and demos.
type Fake struct {
	ShardID string
	Bound   float64
	Records []Record
	Delay   time.Duration // respond after this delay
	Hang    bool          // never respond; wait for ctx cancellation
	Corrupt bool          // claim one more record than actually returned
	Err     error         // fail immediately with this error
	OnQuery func()        // optional hook fired when Query is entered
}

func (f Fake) ID() string { return f.ShardID }

func (f Fake) UpperBound() float64 { return f.Bound }

func (f Fake) Query(ctx context.Context) Result {
	if f.OnQuery != nil {
		f.OnQuery()
	}
	base := Result{ShardID: f.ShardID, Bound: f.Bound}
	if f.Err != nil {
		base.Err = f.Err
		return base
	}
	if f.Delay > 0 {
		select {
		case <-ctx.Done():
			base.Err = ctx.Err()
			return base
		case <-time.After(f.Delay):
		}
	}
	if f.Hang {
		<-ctx.Done()
		base.Err = ctx.Err()
		return base
	}
	base.Records = f.Records
	base.Claimed = len(f.Records)
	if f.Corrupt {
		base.Claimed++
	}
	return base
}
