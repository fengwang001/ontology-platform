// Package sample makes deterministic, chain-consistent sampling
// decisions: the keep/drop verdict depends only on the trace ID hash,
// never on record content, arrival order, or time.
package sample

import (
	"crypto/sha256"
	"encoding/binary"
	"sync/atomic"

	"ontology/record"
)

// Sampler decides per trace chain. It is stateless besides counters and
// safe for concurrent use.
type Sampler struct {
	rate   float64
	hashes atomic.Int64
}

// New returns a Sampler keeping each chain with probability rate
// (clamped to [0,1]).
func New(rate float64) *Sampler {
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	return &Sampler{rate: rate}
}

// Decision is the outcome for one record.
type Decision struct {
	// Keep reports whether the record is retained.
	Keep bool
	// Forced is true when the chain was sampled out but the record was
	// kept because its level is error or above; such records must be
	// marked as belonging to an incomplete chain.
	Forced bool
}

// Decide returns the verdict for one record. Exactly one hash is
// computed per call (see HashCount). An empty trace ID is hashed as-is,
// so all ID-less records share one deterministic fate.
func (s *Sampler) Decide(traceID string, level record.Level) Decision {
	sum := sha256.Sum256([]byte(traceID))
	s.hashes.Add(1)
	keep := float64(binary.BigEndian.Uint64(sum[:8]))/float64(1<<64) < s.rate
	if level >= record.LevelError && !keep {
		return Decision{Keep: true, Forced: true}
	}
	return Decision{Keep: keep}
}

// HashCount is the total number of hash computations performed.
func (s *Sampler) HashCount() int64 { return s.hashes.Load() }
