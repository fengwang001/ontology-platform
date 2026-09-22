// Package canon canonicalizes URLs and decides equivalence.
//
// Equivalent(a, b) holds iff canon(a) and canon(b) are byte-identical,
// which makes equivalence reflexive, symmetric and transitive by
// construction. The normalizer keeps no per-URL state; the only shared
// state is an atomic observability counter of scanned bytes.
package canon

import (
	"errors"
	"strings"
	"sync/atomic"
)

// Mode selects query parameter ordering.
type Mode int

const (
	// ModeOrdered keeps the original parameter order.
	ModeOrdered Mode = iota
	// ModeSorted sorts parameters by key, then value.
	ModeSorted
)

// Config tunes resource limits. Zero values pick defaults.
type Config struct {
	MaxLength   int // max URL length in bytes
	MaxSegments int // max raw path segment count
	MaxParams   int // max query parameter count
	Mode        Mode
}

const (
	defaultMaxLength   = 1 << 20
	defaultMaxSegments = 1 << 15
	defaultMaxParams   = 1 << 15
)

// Resource-limit rejections, distinguishable via errors.Is.
var (
	ErrTooLong         = errors.New("canon: URL exceeds maximum length")
	ErrTooManySegments = errors.New("canon: path exceeds maximum segment count")
	ErrTooManyParams   = errors.New("canon: query exceeds maximum parameter count")
)

// Normalizer canonicalizes URLs. Safe for concurrent use.
type Normalizer struct {
	cfg   Config
	scans atomic.Int64
}

// New returns a Normalizer with the given config.
func New(cfg Config) *Normalizer {
	if cfg.MaxLength <= 0 {
		cfg.MaxLength = defaultMaxLength
	}
	if cfg.MaxSegments <= 0 {
		cfg.MaxSegments = defaultMaxSegments
	}
	if cfg.MaxParams <= 0 {
		cfg.MaxParams = defaultMaxParams
	}
	return &Normalizer{cfg: cfg}
}

// ScanCount reports how many input bytes the normalizer has scanned.
// It is observability-only and never influences results.
func (n *Normalizer) ScanCount() int64 {
	return n.scans.Load()
}

// Normalize returns the canonical form of raw.
func (n *Normalizer) Normalize(raw string) (string, error) {
	s, _, err := n.normalize(raw, true)
	return s, err
}

// Equivalent reports whether a and b canonicalize to the same string.
func (n *Normalizer) Equivalent(a, b string) (bool, error) {
	ca, err := n.Normalize(a)
	if err != nil {
		return false, err
	}
	cb, err := n.Normalize(b)
	if err != nil {
		return false, err
	}
	return ca == cb, nil
}

// normalize is the single-pass pipeline. When counted is false the
// shared scan counter is left untouched (read-only queries).
func (n *Normalizer) normalize(raw string, counted bool) (string, Report, error) {
	var rep Report
	if len(raw) > n.cfg.MaxLength {
		return "", rep, ErrTooLong
	}
	u, err := splitURL(raw)
	if err != nil {
		return "", rep, err
	}
	if u.path != "" && strings.Count(u.path, "/")+1 > n.cfg.MaxSegments {
		return "", rep, ErrTooManySegments
	}
	if u.hasQuery && u.query != "" && strings.Count(u.query, "&")+1 > n.cfg.MaxParams {
		return "", rep, ErrTooManyParams
	}
	var scans int64
	out, rep, err := n.assemble(&u, &scans)
	if counted {
		n.scans.Add(scans)
	}
	return out, rep, err
}
