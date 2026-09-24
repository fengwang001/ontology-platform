package mono

import (
	"errors"
	"sync/atomic"

	"ontology/stack"
)

var (
	ErrNilSequence   = errors.New("mono: nil sequence")
	ErrLimitExceeded = errors.New("mono: sequence length exceeds limit")
	ErrInvalidMaxLen = errors.New("mono: max length must be positive")
)

type Scanner struct {
	maxLen int
	ops    atomic.Int64
}

func NewScanner(maxLen int) (*Scanner, error) {
	if maxLen <= 0 {
		return nil, ErrInvalidMaxLen
	}
	return &Scanner{maxLen: maxLen}, nil
}

func (s *Scanner) NextGreater(values []int) ([]int, error) {
	answers, _, err := s.scan(values, false)
	return answers, err
}

func (s *Scanner) Trace(values []int) ([][]int, error) {
	_, traces, err := s.scan(values, true)
	return traces, err
}

func (s *Scanner) scan(values []int, keepTrace bool) ([]int, [][]int, error) {
	if values == nil {
		return nil, nil, ErrNilSequence
	}
	if len(values) > s.maxLen {
		return nil, nil, ErrLimitExceeded
	}
	answers := make([]int, len(values))
	for i := range answers {
		answers[i] = -1
	}
	traces := make([][]int, 0, len(values))
	indices := stack.New()
	s.ops.Store(0)
	for i, value := range values {
		for {
			top, ok := indices.Top()
			if !ok || values[top] >= value {
				break
			}
			indices.Pop()
			s.ops.Add(1)
			answers[top] = i
		}
		indices.Push(i)
		s.ops.Add(1)
		if keepTrace {
			traces = append(traces, indices.Snapshot())
		}
	}
	return answers, traces, nil
}

func (s *Scanner) operations() int64 {
	return s.ops.Load()
}
