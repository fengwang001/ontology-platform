// Package sample provides consistent-per-trace sampling: the keep decision is
// a pure function of the trace id, so every record on a chain agrees.
package sample

import (
	"crypto/sha256"
	"encoding/binary"
	"sync/atomic"

	"ontology/record"
)

const hashSpace = uint64(1) << 32

// Decider maps trace ids to keep/drop decisions at a fixed rate.
type Decider struct {
	// numerator of keep probability over 2^32; 0 drops all, 2^32 keeps all.
	threshold uint64
	hashCalls uint64
}

// New returns a Decider. rate is clamped to [0,1].
func New(rate float64) *Decider {
	switch {
	case rate <= 0:
		return &Decider{}
	case rate >= 1:
		return &Decider{threshold: hashSpace}
	}
	return &Decider{threshold: uint64(rate * float64(hashSpace))}
}

// Keep reports the deterministic chain decision for a trace id. It performs
// exactly one hash computation per call. SHA-256's first 8 bytes bucket
// uniformly even for highly similar trace ids (FNV-1a clusters those).
func (d *Decider) Keep(traceID string) bool {
	atomic.AddUint64(&d.hashCalls, 1)
	sum := sha256.Sum256([]byte(traceID))
	v := binary.BigEndian.Uint64(sum[:8])
	return v%hashSpace < d.threshold
}

// HashCalls reports the number of hash computations performed.
func (d *Decider) HashCalls() uint64 { return atomic.LoadUint64(&d.hashCalls) }

// Verdict is the per-record outcome.
type Verdict struct {
	// Keep is whether the record must be emitted.
	Keep bool
	// ChainKept is the underlying chain decision (may differ from Keep when
	// an error record is force-kept).
	ChainKept bool
	// ChainIncomplete is true exactly when a chain was dropped but this record
	// is force-kept due to error-or-above level.
	ChainIncomplete bool
}

// Decide combines the chain decision with error-level force retention.
func (d *Decider) Decide(r *record.Record) Verdict {
	chainKept := d.Keep(r.TraceID)
	force := r.Level >= record.Error
	v := Verdict{ChainKept: chainKept, Keep: chainKept || force}
	v.ChainIncomplete = force && !chainKept
	return v
}

// Process applies the verdict to a record: dropped records return (nil,true),
// force-kept error records are tagged chain-incomplete.
func (d *Decider) Process(r *record.Record) (*record.Record, Verdict) {
	v := d.Decide(r)
	if !v.Keep {
		return nil, v
	}
	out := *r
	out.ChainIncomplete = v.ChainIncomplete
	return &out, v
}
