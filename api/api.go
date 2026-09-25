// Package api is the public facade over join: New/Feed/Outputs/SelfCheck
// with decidable sentinel errors. It depends only on join.
package api

import (
	"errors"
	"unicode/utf8"

	"ontology/join"
)

// Side and its two values are aliases of join's sides.
type Side = join.Side

const (
	L = join.L
	R = join.R
)

// Output is one emitted (Key, LVal, RVal) triple; Key is non-NULL.
type Output = join.Output

// Sentinel errors; the three failure classes are pairwise distinct and
// all decidable with errors.Is.
var (
	ErrInvalidSide      = errors.New("api: invalid side: must be L or R")
	ErrInvalidMaxKeyLen = errors.New("api: maxKeyLen must be positive")
	ErrKeyTooLong       = errors.New("api: key length exceeds maxKeyLen")
)

// Joiner validates and ingests events. The zero value is not usable.
type Joiner struct {
	max int
	eng *join.Engine
}

// New creates a Joiner whose non-NULL keys must be at most maxKeyLen
// runes long. A non-positive maxKeyLen is rejected and nil is returned.
func New(maxKeyLen int) (*Joiner, error) {
	if maxKeyLen <= 0 {
		return nil, ErrInvalidMaxKeyLen
	}
	return &Joiner{max: maxKeyLen, eng: join.New()}, nil
}

// Feed validates side and key length (NULL keys are always legal) before
// touching any state, so a rejected call leaves buffers and outputs
// untouched and the Joiner stays usable.
func (j *Joiner) Feed(side Side, key *string, val int64) error {
	if side != L && side != R {
		return ErrInvalidSide
	}
	if key != nil && utf8.RuneCountInString(*key) > j.max {
		return ErrKeyTooLong
	}
	j.eng.Feed(side, key, val)
	return nil
}

// Outputs returns an independent snapshot copy of all outputs so far.
func (j *Joiner) Outputs() []Output {
	return j.eng.Snapshot()
}

// SelfCheck runs the built-in checks of the join engine.
func (j *Joiner) SelfCheck() error {
	return j.eng.SelfCheck()
}
