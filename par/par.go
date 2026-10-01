// Package par normalizes a large buffer in K parallel segments and
// stitches output and offset maps so the result is identical to a
// single streaming pass. See DESIGN.md section 4 for the argument.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

// wsish reports whether b can be part of an undecided tail.
func wsish(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// Normalize splits data at the given cut offsets (sorted, in
// [0,len(data)]), normalizes the segments concurrently, and returns
// the same output and map as one norm pass over all of data.
func Normalize(data []byte, cuts []int, cfg norm.Config) ([]byte, *span.Map, error) {
	k := len(cuts) + 1
	end := make([]int, k) // effective end of segment i
	prev := 0
	for i := 0; i < k-1; i++ {
		e := cuts[i]
		for e > prev && wsish(data[e-1]) {
			e--
		}
		end[i] = e
		prev = e
	}
	end[k-1] = len(data)
	outs := make([][]byte, k)
	maps := make([]*span.Map, k)
	errs := make([]error, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		start := 0
		if i > 0 {
			start = end[i-1]
		}
		segCfg := norm.Config{Policy: norm.Preserve, Strict: cfg.Strict, WSLimit: cfg.WSLimit}
		if end[i] == len(data) { // unique segment reaching EOF applies the policy
			segCfg.Policy = cfg.Policy
		}
		wg.Add(1)
		go func(i, start int, cfg norm.Config) {
			defer wg.Done()
			n := norm.New(cfg)
			if _, err := n.Write(data[start:end[i]]); err != nil {
				errs[i] = rebase(err, start)
				return
			}
			if err := n.Close(); err != nil {
				errs[i] = rebase(err, start)
				return
			}
			outs[i], maps[i] = n.Output(), n.Map()
		}(i, start, segCfg)
	}
	wg.Wait()
	m := &span.Map{Orig: len(data)}
	var out []byte
	for i := 0; i < k; i++ {
		if errs[i] != nil {
			return nil, nil, errs[i]
		}
		origBase := 0
		if i > 0 {
			origBase = end[i-1]
		}
		m.Append(maps[i], origBase, m.Out)
		out = append(out, outs[i]...)
		m.Out += len(outs[i])
	}
	if cfg.OutLimit > 0 && m.Out > cfg.OutLimit {
		return nil, nil, &norm.Error{Err: norm.ErrOutLimit, Orig: m.ToOrig(cfg.OutLimit)}
	}
	if cfg.Policy == norm.EnsureOne && m.Out > 0 && out[m.Out-1] != '\n' {
		out = append(out, '\n') // the policy segment saw an empty local output
		m.Appended++
		m.Out++
	}
	return out, m, nil
}

func rebase(err error, base int) error {
	if e, ok := err.(*norm.Error); ok {
		return &norm.Error{Err: e.Err, Orig: e.Orig + base}
	}
	return err
}
