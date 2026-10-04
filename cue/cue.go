// Package cue validates subtitle cues.
package cue

import "errors"

const (
	MaxTime  = 1_000_000_000
	MaxBytes = 200
	MaxCues  = 10_000
	MinDmin  = 1
	MaxDmin  = 1_000_000
)

var ErrInvalidParam = errors.New("cue: invalid parameter")

type Kind int

const (
	// KindTime: start/end out of range or start >= end.
	KindTime Kind = iota + 1
	// KindDuration: end-start < dmin.
	KindDuration
	// KindOrder: start smaller than previous cue's start, or overlap.
	KindOrder
	// KindText: empty or longer than MaxBytes bytes.
	KindText
)

type Cue struct {
	Start int64
	End   int64
	Text  string
}

type CueError struct {
	Index int
	Kind  Kind
}

func (e *CueError) Error() string { return "cue: invalid cue" }

func (e *CueError) Is(target error) bool {
	_, ok := target.(*CueError)
	return ok
}

// Validate checks dmin and all cues, returning a *CueError for the first
// offending index. Within one cue, the kind priority is
// KindTime > KindDuration > KindOrder > KindText.
func Validate(dmin int64, cues []Cue) error {
	if dmin < MinDmin || dmin > MaxDmin {
		return ErrInvalidParam
	}
	if len(cues) > MaxCues {
		return ErrInvalidParam
	}
	var prevEnd int64
	for i, c := range cues {
		switch {
		case c.Start < 0 || c.Start > MaxTime || c.End < 0 || c.End > MaxTime || c.Start >= c.End:
			return &CueError{Index: i, Kind: KindTime}
		case c.End-c.Start < dmin:
			return &CueError{Index: i, Kind: KindDuration}
		case i > 0 && (c.Start < cues[i-1].Start || c.Start < prevEnd):
			return &CueError{Index: i, Kind: KindOrder}
		case len(c.Text) == 0 || len(c.Text) > MaxBytes:
			return &CueError{Index: i, Kind: KindText}
		}
		prevEnd = c.End
	}
	return nil
}
