// Package enc implements the incremental changelog encoder: a bounded
// dictionary with boundary resets, run-length encoding of consecutive ref
// hits, and the inverse decoder. It depends only on dict.
package enc

import (
	"errors"

	"ontology/dict"
)

// Sentinel errors. They are intentionally distinct so callers can decide
// what failed with errors.Is.
var (
	// ErrEmptyValue rejects Append(""); empty values are not encodable.
	ErrEmptyValue = errors.New("enc: appended value must be non-empty")
	// ErrBadToken rejects any stream Decode cannot consume safely: a ref to
	// an unknown code, a put with code outside [0,K), or a ref with m < 1.
	ErrBadToken = errors.New("enc: malformed token")
)

// Kind identifies a token kind.
type Kind int

const (
	KindReset Kind = iota
	KindPut
	KindRef
)

// Token is one entry of the encoded stream.
//   - Reset: empty token, clears the dictionary on both ends.
//   - Put:   Code/Value bind a new value; Value must be non-empty.
//   - Ref:   Code names a bound value; Count is the run length (>=1), so a
//     Count of 2+ is the RLE form of one ref repeated Count times.
type Token struct {
	Kind  Kind
	Code  int
	Value string
	Count int
}

// ResetToken builds a boundary-reset token.
func ResetToken() Token { return Token{Kind: KindReset} }

// PutToken builds a dictionary insertion token.
func PutToken(code int, value string) Token {
	return Token{Kind: KindPut, Code: code, Value: value}
}

// RefToken builds a reference token; count must be >= 1.
func RefToken(code, count int) Token {
	return Token{Kind: KindRef, Code: code, Count: count}
}

// Coder incrementally appends values to an encoded token stream.
type Coder struct {
	d   *dict.Dict
	tok []Token
}

// NewCoder creates a coder with dictionary capacity k; the caller guarantees k > 0.
func NewCoder(k int) *Coder {
	return &Coder{d: dict.New(k)}
}

// Cap returns the fixed dictionary capacity.
func (c *Coder) Cap() int { return c.d.Cap() }

// Append encodes one value in arrival order. On a miss it emits put(code),
// after a reset+clear if the dictionary is full; on a hit it extends (or
// opens) the trailing ref run. put and reset are never merged and break runs.
func (c *Coder) Append(value string) error {
	if value == "" {
		return ErrEmptyValue
	}
	if code, ok := c.d.Lookup(value); ok {
		c.emitRef(code)
		return nil
	}
	if c.d.Full() {
		c.tok = append(c.tok, ResetToken())
		c.d.Reset()
	}
	code := c.d.Put(value)
	c.tok = append(c.tok, PutToken(code, value))
	return nil
}

// emitRef merges into the trailing ref of the same code, otherwise opens a run.
func (c *Coder) emitRef(code int) {
	if n := len(c.tok); n > 0 && c.tok[n-1].Kind == KindRef && c.tok[n-1].Code == code {
		c.tok[n-1].Count++
		return
	}
	c.tok = append(c.tok, RefToken(code, 1))
}

// Tokens returns a point-in-time snapshot copy of the token stream.
func (c *Coder) Tokens() []Token {
	out := make([]Token, len(c.tok))
	copy(out, c.tok)
	return out
}

// Decode expands tokens back to the original values. It works on a fresh local
// dictionary, so a malformed stream leaves the receiver untouched: validation
// happens against local state and the error discards all local work.
func (c *Coder) Decode(tokens []Token) ([]string, error) {
	d := dict.New(c.d.Cap())
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		switch t.Kind {
		case KindReset:
			d.Reset()
		case KindPut:
			if t.Code < 0 || t.Code >= c.d.Cap() {
				return nil, ErrBadToken
			}
			d.Set(t.Code, t.Value)
			out = append(out, t.Value)
		case KindRef:
			if t.Count < 1 {
				return nil, ErrBadToken
			}
			v, ok := d.At(t.Code)
			if !ok {
				return nil, ErrBadToken
			}
			for i := 0; i < t.Count; i++ {
				out = append(out, v)
			}
		default:
			return nil, ErrBadToken
		}
	}
	return out, nil
}
