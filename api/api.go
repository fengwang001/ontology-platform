// Package api is the public facade: configuration, incremental append,
// token snapshots and decoding. It depends only on enc.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/enc"
)

// ErrBadConfig is returned when New receives a non-positive capacity. It is
// distinct from enc.ErrEmptyValue and enc.ErrBadToken.
var ErrBadConfig = errors.New("api: dictionary capacity K must be > 0")

// Engine is the concurrent-safe encoder facade.
type Engine struct {
	mu sync.RWMutex
	c  *enc.Coder
}

// New creates an engine whose dictionary holds K distinct values per epoch.
func New(K int) (*Engine, error) {
	if K <= 0 {
		return nil, ErrBadConfig
	}
	return &Engine{c: enc.NewCoder(K)}, nil
}

// Append encodes one value. Empty values are rejected before any state change.
func (e *Engine) Append(value string) error {
	if value == "" {
		return enc.ErrEmptyValue
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.c.Append(value)
}

// Tokens returns a point-in-time snapshot copy of the token stream.
func (e *Engine) Tokens() []enc.Token {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.Tokens()
}

// Decode expands a token stream into the original values. It takes only a
// read lock because it never mutates the encoder's dictionary.
func (e *Engine) Decode(tokens []enc.Token) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.Decode(tokens)
}

// canonical is the NOTES.md worked example and its lossless RLE expansion:
// every ref(code,m) is written out as m single refs.
var canonical = []string{"a", "a", "a", "b", "c", "d", "e", "a"}

var canonicalFlat = []enc.Token{
	enc.PutToken(0, "a"), enc.RefToken(0, 1), enc.RefToken(0, 1),
	enc.PutToken(1, "b"), enc.PutToken(2, "c"), enc.PutToken(3, "d"),
	enc.ResetToken(), enc.PutToken(0, "e"), enc.PutToken(1, "a"),
}

// SelfCheck verifies the four invariants over built-in sequences using fresh
// engines, so the receiver is never modified.
func (e *Engine) SelfCheck() error {
	g, err := New(4)
	if err != nil {
		return err
	}
	for _, v := range canonical {
		if err := g.Append(v); err != nil {
			return err
		}
	}
	tok := g.Tokens()
	// Invariant 1: round trip is element-wise identical.
	got, err := g.Decode(tok)
	if err != nil || !reflect.DeepEqual(got, canonical) {
		return errRoundTrip
	}
	// Invariant 2: emitted codes stay in [0,K) and run lengths are >=1.
	for _, t := range tok {
		if t.Code < 0 || t.Code >= 4 || (t.Kind == enc.KindRef && t.Count < 1) {
			return errBounds
		}
	}
	// Invariant 3: expanded RLE stream equals the uncompressed emission.
	flat := expandRefs(tok)
	if !reflect.DeepEqual(flat, canonicalFlat) {
		return errRLE
	}
	return checkRejection()
}

// expandRefs rewrites every ref(code,m) into m single refs.
func expandRefs(tokens []enc.Token) []enc.Token {
	var out []enc.Token
	for _, t := range tokens {
		if t.Kind != enc.KindRef {
			out = append(out, t)
			continue
		}
		for i := 0; i < t.Count; i++ {
			out = append(out, enc.RefToken(t.Code, 1))
		}
	}
	return out
}

// checkRejection covers invariant 4: rejected operations leave no trace,
// errors are the three distinct sentinels, and the engine stays usable.
func checkRejection() error {
	g, _ := New(2)
	if err := g.Append(""); !errors.Is(err, enc.ErrEmptyValue) {
		return errReject
	}
	before := g.Tokens()
	bad := [][]enc.Token{
		{enc.RefToken(3, 1)},
		{enc.PutToken(5, "z")},
		{enc.PutToken(0, "z"), enc.RefToken(0, 0)},
	}
	for _, ts := range bad {
		if _, err := g.Decode(ts); !errors.Is(err, enc.ErrBadToken) {
			return errReject
		}
	}
	if !reflect.DeepEqual(g.Tokens(), before) || g.Append("z") != nil {
		return errReject
	}
	out, err := g.Decode(g.Tokens())
	if err != nil || len(out) != 1 || out[0] != "z" {
		return errReject
	}
	return nil
}

var (
	errRoundTrip = errors.New("api: round trip mismatch")
	errBounds    = errors.New("api: code or run length out of bounds")
	errRLE       = errors.New("api: RLE expansion is not lossless")
	errReject    = errors.New("api: rejected operation changed state or error kind")
)
