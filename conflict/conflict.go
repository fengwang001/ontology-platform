// Package conflict parses, resolves, normalizes and re-renders text
// documents that contain diff3-style conflict marker blocks.
package conflict

import (
	"errors"
	"fmt"
	"sync"
)

// Choice selects how a conflict block is resolved.
type Choice int

const (
	// Ours keeps the lines between the '<' and '|'/'=' markers.
	Ours Choice = iota
	// Theirs keeps the lines between the '=' and '>' markers.
	Theirs
	// Both keeps ours lines followed by theirs lines (no dedup).
	Both
	// Base keeps the base lines; fails with ErrNoBase if the block
	// has no '|' marker.
	Base
	// Custom replaces the block with caller supplied lines.
	Custom
)

// Sentinel errors. Parse errors that carry a line number are returned as
// *LineError wrapping one of these sentinels; use errors.Is to classify.
var (
	ErrBadL         = errors.New("conflict: marker length L must be >= 7")
	ErrStray        = errors.New("conflict: stray marker line outside block")
	ErrNested       = errors.New("conflict: nested block start inside block")
	ErrOrder        = errors.New("conflict: marker lines out of order")
	ErrUnterminated = errors.New("conflict: unterminated conflict block")
	ErrNoNewline    = errors.New("conflict: last line missing trailing newline")
	ErrNoSuchBlock  = errors.New("conflict: block index out of range")
	ErrNoBase       = errors.New("conflict: block has no base section")
	ErrBadLine      = errors.New("conflict: custom line contains newline")
	ErrTooLarge     = errors.New("conflict: rendered line count exceeds MaxLines")
	ErrBadChoice    = errors.New("conflict: unknown resolve choice")
)

// LineError is a parse error annotated with a 1-based line number.
type LineError struct {
	Err  error // one of the sentinel errors above
	Line int   // 1-based line number
}

func (e *LineError) Error() string {
	return fmt.Sprintf("%s (line %d)", e.Err, e.Line)
}

func (e *LineError) Unwrap() error { return e.Err }

// segment is one body segment or one conflict block.
type segment struct {
	isBlock bool

	// body segment
	lines []string

	// conflict block
	ours, base, theirs []string
	hasBase            bool
	lOpen              string // label of the '<' marker line
	lBase              string // label of the '|' marker line
	lSep               string // label of the '=' marker line
	lClose             string // label of the '>' marker line
}

// Doc is a parsed conflict document. All methods are safe for concurrent
// use; concurrent calls behave as if executed in some serial order.
type Doc struct {
	mu       sync.Mutex
	segs     []segment
	maxLines int
}

// Parse parses text into a Doc. L is the marker length (must be >= 7).
// maxLines bounds the number of rendered lines (marker lines included);
// it must be in [1, 10^6]. Syntax is validated before the size limit.
func Parse(text string, L int, maxLines int) (*Doc, error) {
	if L < 7 {
		return nil, ErrBadL
	}
	segs, err := parse(text, L)
	if err != nil {
		return nil, err
	}
	d := &Doc{segs: segs, maxLines: maxLines}
	if d.renderLinesLocked() > maxLines {
		return nil, ErrTooLarge
	}
	return d, nil
}

// Blocks reports the current number of unresolved conflict blocks.
func (d *Doc) Blocks() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.blocksLocked()
}

// Resolve resolves the i-th (0-based) unresolved block. After a block is
// resolved, the indices of the following blocks shift down by one.
// A rejected resolve leaves the document unchanged.
func (d *Doc) Resolve(i int, choice Choice, custom []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resolveLocked(i, choice, custom)
}

// Normalize moves common prefix/suffix lines out of every block.
func (d *Doc) Normalize() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.normalizeLocked()
}

// Render renders the document back to text.
func (d *Doc) Render() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.renderLocked()
}

// MarkerLen returns the marker length L' that Render currently uses.
func (d *Doc) MarkerLen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.markerLenLocked()
}
