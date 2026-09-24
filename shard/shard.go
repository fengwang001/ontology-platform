// Package shard defines the shard interface and an injectable fake shard.
package shard

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Record is one item contributed by a shard. Valid=false means only partial
// fields were returned (Score missing); it still contributes to Count.
type Record struct {
	ID    string
	Score int64
	Valid bool
}

// Response is one fetch result. Seq identifies the attempt; retries reuse it.
type Response struct {
	Seq     int64
	Count   int64
	Records []Record
	Bound   int64
	OK      bool
}

// Shard is the injectable remote interface. No real network is involved.
type Shard interface {
	ID() string
	Fetch(ctx context.Context, seq int64) (Response, error)
}

var (
	// ErrCorrupt marks a self-inconsistent response.
	ErrCorrupt = errors.New("shard: corrupt response")
	// ErrRetryable means the response is usable but the caller should retry
	// with the same seq (simulates a retry stacking on top of a delivered one).
	ErrRetryable = errors.New("shard: retry suggested")
)

// Validate rejects responses whose claimed count mismatches the payload or
// whose OK flag is clear.
func Validate(r Response) error {
	if !r.OK || r.Count != int64(len(r.Records)) {
		return ErrCorrupt
	}
	return nil
}

// Corrupt selects the injected corruption mode.
type Corrupt int

const (
	CorruptNone Corrupt = iota
	CorruptCount
	CorruptNotOK
)

// Config configures a Fake.
type Config struct {
	ID      string
	Records []Record
	Bound   int64
	Delay   time.Duration
	Hang    bool
	Corrupt Corrupt
	Replay  bool
}

// Fake is an in-memory shard with latency / timeout / corruption / replay.
type Fake struct {
	cfg   Config
	mu    sync.Mutex
	calls int
}

// NewFake builds a fake shard; an empty ID is legal.
func NewFake(cfg Config) *Fake { return &Fake{cfg: cfg} }

// ID returns the shard identifier (may be "").
func (f *Fake) ID() string { return f.cfg.ID }

// Calls reports how many fetches were ever started against this shard.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Fetch implements Shard.
func (f *Fake) Fetch(ctx context.Context, seq int64) (Response, error) {
	f.mu.Lock()
	f.calls++
	replay := f.cfg.Replay && f.calls == 1
	f.mu.Unlock()

	if f.cfg.Hang {
		<-ctx.Done()
		return Response{}, ctx.Err()
	}
	if f.cfg.Delay > 0 {
		t := time.NewTimer(f.cfg.Delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return Response{}, ctx.Err()
		case <-t.C:
		}
	}

	recs := make([]Record, len(f.cfg.Records))
	copy(recs, f.cfg.Records)
	resp := Response{Seq: seq, Count: int64(len(recs)), Records: recs, Bound: f.cfg.Bound, OK: true}
	switch f.cfg.Corrupt {
	case CorruptCount:
		resp.Count++
	case CorruptNotOK:
		resp.OK = false
	}
	if replay {
		return resp, ErrRetryable
	}
	return resp, nil
}
