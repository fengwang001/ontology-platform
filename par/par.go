// Package par normalizes a large buffer in K goroutines and stitches the
// outputs and offset maps back together exactly as a single run would.
package par

import (
	"errors"
	"sync"

	"ontology/norm"
	"ontology/span"
)

var (
	// ErrBadK reports K outside 1..8.
	ErrBadK = errors.New("par: K must be in 1..8")
	// ErrBadCut reports an out-of-range or non-increasing cut.
	ErrBadCut = errors.New("par: invalid cut offsets")
)

// Result is the parallel normalization outcome.
type Result struct {
	Out []byte
	Map *span.Map
}

// Split normalizes data with K goroutines. cuts are raw byte offsets
// (exclusive of 0 and len(data)); K = len(cuts)+1 and must be 1..8.
func Split(data []byte, cuts []int, cfg norm.Config) (Result, error) {
	K := len(cuts) + 1
	if K < 1 || K > 8 {
		return Result{}, ErrBadK
	}
	starts := make([]int, K)
	for k := 1; k < K; k++ {
		c := cuts[k-1]
		if c <= 0 || c >= len(data) || c <= cuts[k-2] {
			return Result{}, ErrBadCut
		}
		starts[k] = align(data, c, cfg.MaxWS)
	}

	type piece struct {
		out []byte
		m   *span.Map
		err error
	}
	pieces := make([]piece, K)
	var wg sync.WaitGroup
	for k := 0; k < K; k++ {
		lo := starts[k]
		hi := len(data)
		if k+1 < K {
			hi = starts[k+1]
		}
		pcfg := cfg
		if k+1 < K {
			pcfg.Policy = norm.Keep
		}
		wg.Add(1)
		go func(k, lo, hi int, cfg norm.Config) {
			defer wg.Done()
			out, m, err := norm.Normalize(data[lo:hi], cfg)
			pieces[k] = piece{out, m, err}
		}(k, lo, hi, pcfg)
	}
	wg.Wait()

	var out []byte
	origBounds := make([]int, K)
	outBounds := make([]int, K)
	maps := make([]*span.Map, K)
	for k := 0; k < K; k++ {
		if pieces[k].err != nil {
			return Result{Out: out}, shiftErr(pieces[k].err, starts[k], len(out))
		}
		origBounds[k] = starts[k]
		outBounds[k] = len(out)
		maps[k] = pieces[k].m
		out = append(out, pieces[k].out...)
	}
	m := span.Concat(maps, origBounds, outBounds, len(data), len(out))
	return Result{Out: out, Map: m}, nil
}

// N splits data into K roughly equal raw pieces and normalizes in parallel.
func N(data []byte, K int, cfg norm.Config) (Result, error) {
	if K < 1 || K > 8 {
		return Result{}, ErrBadK
	}
	if K == 1 || len(data) == 0 {
		out, m, err := norm.Normalize(data, cfg)
		return Result{Out: out, Map: m}, err
	}
	cuts := make([]int, K-1)
	for k := range cuts {
		cuts[k] = (k + 1) * len(data) / K
	}
	return Split(data, cuts, cfg)
}

// align moves a raw cut point back to the start of the line containing
// it: one past the nearest preceding '\n', or 0 if there is none.
func align(data []byte, c, maxWS int) int {
	min := 0
	if maxWS > 0 && c-maxWS-2 > min {
		min = c - maxWS - 2
	}
	for i := c - 1; i >= min; i-- {
		if data[i] == '\n' {
			return i + 1
		}
	}
	if min > 0 {
		for i := min - 1; i >= 0; i-- {
			if data[i] == '\n' {
				return i + 1
			}
		}
	}
	return 0
}

func shiftErr(err error, origShift, outShift int) error {
	switch e := err.(type) {
	case *norm.NULOffsetError:
		return &norm.NULOffsetError{Offset: e.Offset + origShift}
	case *norm.WhitespaceLimitError:
		return &norm.WhitespaceLimitError{Offset: e.Offset + origShift, Limit: e.Limit}
	case *norm.OutputLimitError:
		return &norm.OutputLimitError{Offset: e.Offset + origShift, Limit: e.Limit}
	}
	return err
}
