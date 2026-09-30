// Package par splits input at arbitrary byte offsets, normalizes K fragments
// concurrently, and joins outputs and maps identically to one stream.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

type frag struct {
	out     []byte
	segs    []span.Seg
	rawTail int // undecided raw original bytes at this fragment's tail
}

// Normalize splits input at cuts (k=len(cuts)+1 ranges) and joins results.
func Normalize(input []byte, cuts []int, cfg norm.Config) ([]byte, *span.Map, error) {
	bounds := append(append([]int{0}, cuts...), len(input))
	k := len(bounds) - 1
	fs := make([]frag, k)
	errs := make([]error, k)
	var wg sync.WaitGroup
	for g := 0; g < k; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			n := norm.New(cfg)
			n.Fragment = true
			if _, err := n.Write(input[bounds[g]:bounds[g+1]]); err != nil {
				errs[g] = err
				return
			}
			if err := n.Close(); err != nil {
				errs[g] = err
				return
			}
			fs[g] = frag{
				out:     append([]byte(nil), n.Output()...),
				segs:    append([]span.Seg(nil), n.Map().Segs()...),
				rawTail: n.RawTail(),
			}
		}(g)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, nil, e
		}
	}
	return join(fs, bounds, cfg)
}

type rawByte struct {
	orig int
	b    byte
}

// join copies every segment translated into global coordinates, holding each
// fragment's raw tail until the next fragment proves \r\n vs lone \r and
// trailing vs middle whitespace.
func join(fs []frag, bounds []int, cfg norm.Config) ([]byte, *span.Map, error) {
	var out []byte
	mp := &span.Map{}
	lastI, lastO := 0, 0
	var pending []rawByte

	add := func(o, i, ol, il int) { mp.Add(o, i, ol, il) }
	gapTo := func(i int) {
		if lastI < i {
			add(lastO, lastI, 0, i-lastI)
			lastI = i
		}
	}
	emitGap := func(i, n int) { gapTo(i); add(lastO, i, 0, n); lastI = i + n }
	emitByte := func(i int, b byte) {
		gapTo(i)
		out = append(out, b)
		add(lastO, i, 1, 1)
		lastO, lastI = lastO+1, i+1
	}
	settle := func(nl bool) {
	var after []rawByte
	pastCR := false
		for _, p := range pending {
			if p.b == '\r' {
				pastCR = true
				if nl {
					emitGap(p.orig, 1)
				} else {
					emitByte(p.orig, '\n')
				}
				continue
			}
			if pastCR {
				after = append(after, p)
				continue
			}
			if nl {
				emitGap(p.orig, 1)
			} else {
				emitByte(p.orig, p.b)
			}
		}
		pending = after
	}

	for g, f := range fs {
		base := bounds[g]
		rawFrom := bounds[g+1] - rawTailOrig(f)
		for si, s := range f.segs {
			if s.ILen == 0 {
				continue // no inserted segments before end policy
			}
			sI := base + s.I
			if s.OLen == 0 {
				// A deleted run followed (in this fragment) by a newline means
				// any open pending raw run is line-trailing whitespace.
				nl := false
				if si+1 < len(f.segs) {
					nx := f.segs[si+1]
					nl = nx.OLen > 0 && f.out[nx.O] == '\n'
				} else if len(fs) > g+1 {
					nf := fs[g+1]
					nl = len(nf.out) > 0 && nf.out[0] == '\n'
				}
				if len(pending) > 0 {
					settle(nl)
				}
				emitGap(sI, s.ILen)
				continue
			}
			for k := 0; k < s.OLen; k++ {
				orig := sI + k
				b := f.out[s.O+k]
				isRaw := orig >= rawFrom
				if isRaw {
					pending = append(pending, rawByte{orig, b})
					continue
				}
				if len(pending) > 0 && (b == ' ' || b == '\t') {
					pending = append(pending, rawByte{orig, b})
					continue
				}
				if len(pending) > 0 {
					settle(b == '\n')
				}
				emitByte(orig, b)
			}
		}
	}
	settle(false) // lone \r -> newline
	for _, p := range pending { // spaces on both sides of that \r are trailing
		emitGap(p.orig, 1)
	}
	out = norm.FinishTail(out, mp, cfg.EndPolicy)
	return out, mp, nil
}

func hasPendingCR(ps []rawByte) bool {
	for _, p := range ps {
		if p.b == '\r' {
			return true
		}
	}
	return false
}

// rawTailOrig counts source bytes of the fragment's undecided tail. The tail
// is kept verbatim (1:1), so it equals the matching output suffix length.
func rawTailOrig(f frag) int {
	k := len(f.out)
	for k > 0 && (f.out[k-1] == ' ' || f.out[k-1] == '\t') {
		k--
	}
	if k > 0 && f.out[k-1] == '\r' {
		k--
	}
	return len(f.out) - k
}
