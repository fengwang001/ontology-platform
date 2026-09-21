// Package fence implements allocation and validation of fencing tokens.
//
// A fencing token is a monotonically increasing number handed out on every
// lease grant. Downstream writes must carry a token; any token smaller than
// the highest token already accepted is rejected, so a stale holder that
// lost its lease cannot corrupt state.
package fence

import (
	"errors"
	"fmt"
	"sync"
)

// Token is a fencing token. The zero value is never a valid allocated token.
type Token uint64

// ErrStale is returned (wrapped by *StaleError) when a write carries a token
// smaller than the current high-water mark.
var ErrStale = errors.New("fence: stale token")

// StaleError describes a rejected write whose token fell behind the
// high-water mark.
type StaleError struct {
	Token     Token
	Watermark Token
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("fence: token %d is stale, high-water mark is %d", e.Token, e.Watermark)
}

// Is reports that a StaleError matches ErrStale.
func (e *StaleError) Is(target error) bool { return target == ErrStale }

// Fencer allocates strictly increasing tokens and tracks the high-water
// mark of accepted tokens for a single resource. It is safe for concurrent
// use.
type Fencer struct {
	mu        sync.Mutex
	last      Token // last allocated token
	watermark Token // max token ever allocated or accepted
}

// New returns a Fencer whose first allocated token is 1.
func New() *Fencer { return &Fencer{} }

// Allocate returns the next token, strictly greater than every token
// previously returned by this Fencer, and raises the high-water mark to it.
func (f *Fencer) Allocate() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last++
	if f.last > f.watermark {
		f.watermark = f.last
	}
	return f.last
}

// Validate accepts a write carrying token t. It succeeds iff t is not
// smaller than the current high-water mark; on success the mark is raised
// to t. A smaller token is rejected with *StaleError.
func (f *Fencer) Validate(t Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t < f.watermark {
		return &StaleError{Token: t, Watermark: f.watermark}
	}
	f.watermark = t
	return nil
}

// Watermark returns the current high-water mark: the maximum token ever
// allocated or accepted.
func (f *Fencer) Watermark() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watermark
}

// Last returns the most recently allocated token, or 0 if none.
func (f *Fencer) Last() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}
