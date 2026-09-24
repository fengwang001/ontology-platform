// Package par splits input at arbitrary byte offsets, normalizes chunks
// concurrently, and stitches their outputs and offset maps. The result is
// byte-for-byte identical to single-stream normalization with the same
// norm.Config end policy.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

// Result is the stitched normalization output.
type Result struct {
	Out []byte
	Map *span.Map
}

// Run splits input into k consecutive chunks (clamped to 1..8), normalizes
// them in goroutines, and stitches them before applying the end policy once.
func Run(cfg norm.Config, input []byte, k int) (Result, error) {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	if k > len(input) {
		k = len(input)
	}
	if k == 0 {
		k = 1
	}
	bounds := bounds(len(input), k)
	frags := make([]norm.Fragment, len(bounds))
	var wg sync.WaitGroup
	for j := range bounds {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			f, err := norm.Process(cfg, input[bounds[j][0]:bounds[j][1]])
			if err == nil {
				frags[j] = f
			} else {
				frags[j].Err = err
			}
		}(j)
	}
	wg.Wait()
	for _, f := range frags {
		if f.Err != nil {
			return Result{}, f.Err
		}
	}

	m := span.New()
	var out []byte
	penR, lastEnd := false, 0
	for j, f := range frags {
		start, end := bounds[j][0], bounds[j][1]
		switch {
		case penR && start < end && input[start] == '\n':
			// \r|\n: the first run of f is the \r-anchored \n; keep it,
			// drop this input \n byte (no run to add).
		case penR:
			if err := nl(&out, m, start-1, cfg); err != nil {
				return Result{}, err
			}
		}
		penR = false
		ob := len(out)
		for t := 0; t < f.Map.Runs(); t++ {
			r := f.Map.RunAt(t)
			m.Add(start+r.Orig, ob+r.Out, r.Len)
		}
		out = append(out, f.Out...)
		penR = f.PenR
		lastEnd = end
	}
	if penR {
		if err := nl(&out, m, lastEnd-1, cfg); err != nil {
			return Result{}, err
		}
	}
	out, fatal := norm.ApplyEnd(cfg, out, m)
	if fatal {
		return Result{}, &norm.OffsetError{Err: norm.ErrOutLimit, Offset: len(input)}
	}
	return Result{Out: out, Map: m}, nil
}

func nl(out *[]byte, m *span.Map, anchor int, cfg norm.Config) error {
	if cfg.MaxOut > 0 && len(*out) >= cfg.MaxOut {
		return &norm.OffsetError{Err: norm.ErrOutLimit, Offset: anchor}
	}
	*out = append(*out, '\n')
	m.Add(anchor, len(*out)-1, 1)
	return nil
}

func bounds(n, k int) [][2]int {
	b := make([][2]int, k)
	step, rem, pos := n/k, n%k, 0
	for i := 0; i < k; i++ {
		size := step
		if i < rem {
			size++
		}
		b[i] = [2]int{pos, pos + size}
		pos += size
	}
	return b
}
