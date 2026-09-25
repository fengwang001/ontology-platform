// Package stream is the public facade: it wires hashfam, sketch, bound and
// topk into one counter with Add/Estimate/HeavyHitters/Merge/SelfCheck.
package stream

import (
	"errors"

	"ontology/bound"
	"ontology/sketch"
	"ontology/topk"
)

// ErrCapacity rejects an operation that would push the total count past
// the configured limit. The stream stays usable afterwards.
var ErrCapacity = errors.New("stream: total count limit exceeded")

// Config configures a Stream. If Width or Depth is non-zero, both are used
// directly; otherwise Epsilon and Delta (both in (0,1)) determine them.
// Phi is the heavy-hitter threshold fraction. MaxTotal caps the total
// count; zero means unlimited.
type Config struct {
	Width, Depth int
	Epsilon      float64
	Delta        float64
	Phi          float64
	MaxTotal     uint64
}

// Stream couples a sketch with a heavy-hitter detector.
type Stream struct {
	sk  *sketch.Sketch
	det *topk.Detector
	p   bound.Params
	max uint64
}

// New builds a Stream from cfg, rejecting invalid parameters.
func New(cfg Config) (*Stream, error) {
	var p bound.Params
	var err error
	if cfg.Width != 0 || cfg.Depth != 0 {
		p, err = bound.New(cfg.Width, cfg.Depth)
	} else {
		p, err = bound.FromEpsilonDelta(cfg.Epsilon, cfg.Delta)
	}
	if err != nil {
		return nil, err
	}
	sk, err := sketch.New(p.W, p.D)
	if err != nil {
		return nil, err
	}
	det, err := topk.NewDetector(sk, p, cfg.Phi)
	if err != nil {
		return nil, err
	}
	return &Stream{sk: sk, det: det, p: p, max: cfg.MaxTotal}, nil
}

// Add counts n occurrences of key. It fails atomically: a rejected Add
// changes no cell and no statistic.
func (s *Stream) Add(key string, n uint64) error {
	if key == "" {
		return sketch.ErrEmptyKey
	}
	if s.max > 0 && (s.sk.Total() > s.max || n > s.max-s.sk.Total()) {
		return ErrCapacity
	}
	return s.det.Add(key, n)
}

// Estimate returns an upper bound on the true count of key.
func (s *Stream) Estimate(key string) (uint64, error) { return s.sk.Estimate(key) }

// HeavyHitters returns the sorted keys whose estimate exceeds phi*Total.
func (s *Stream) HeavyHitters() []string { return s.det.HeavyHitters() }

// Merge folds other into s. Parameter mismatch or capacity overflow is
// rejected before any cell changes, leaving both streams intact.
func (s *Stream) Merge(other *Stream) error {
	if s.p != other.p {
		return sketch.ErrIncompatible
	}
	t, o := s.sk.Total(), other.sk.Total()
	if s.max > 0 && (t > s.max || o > s.max-t) {
		return ErrCapacity
	}
	if err := s.sk.Merge(other.sk); err != nil {
		return err
	}
	s.det.Merge(other.det)
	return nil
}

// Cells returns a flat copy of the counting matrix, for inspection.
func (s *Stream) Cells() []uint64 {
	var out []uint64
	for r := 0; r < s.p.D; r++ {
		out = append(out, s.sk.Row(r)...)
	}
	return out
}

// SelfCheck verifies matrix dimensions against the parameters, non-negative
// cells, and that every row sums to the total number of insertions.
func (s *Stream) SelfCheck() error {
	if w, d := s.sk.Dims(); w != s.p.W || d != s.p.D {
		return sketch.ErrCorrupt
	}
	return s.sk.SelfCheck()
}
