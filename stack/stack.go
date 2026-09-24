// Package stack represents and normalizes sampled call stacks.
package stack

import "errors"

// ErrEmpty is returned when a stack has no frames at all.
var ErrEmpty = errors.New("stack: empty stack")

// DefaultMaxDepth bounds frames kept per stack when no limit is given.
const DefaultMaxDepth = 64

// Stack is a normalized call stack, root (oldest caller) first.
type Stack struct {
	Frames    []string
	Truncated bool // frames beyond the depth limit were dropped
}

// Depth returns the number of frames.
func (s Stack) Depth() int { return len(s.Frames) }

// Leaf returns the innermost frame name.
func (s Stack) Leaf() string { return s.Frames[len(s.Frames)-1] }

// Normalize dedups consecutive identical frames and enforces maxDepth.
// An empty input is rejected with ErrEmpty; empty frame names are legal.
// Depth exactly equal to maxDepth is kept; one more frame truncates.
func Normalize(frames []string, maxDepth int) (Stack, error) {
	if len(frames) == 0 {
		return Stack{}, ErrEmpty
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	out := make([]string, 0, min(len(frames), maxDepth))
	truncated := false
	for _, f := range frames {
		if n := len(out); n > 0 && out[n-1] == f {
			continue // collapse consecutive duplicates
		}
		if len(out) == maxDepth {
			truncated = true
			break
		}
		out = append(out, f)
	}
	return Stack{Frames: out, Truncated: truncated}, nil
}
