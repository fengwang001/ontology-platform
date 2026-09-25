// Package par normalizes a buffer split at arbitrary byte offsets into
// concurrently processed segments; stitching is identical to a single pass.
package par

import (
	"errors"
	"sync"

	"ontology/norm"
	"ontology/span"
)

var ErrCuts = errors.New("par: cut points must be sorted and within [0, len(buf)]")

type seg struct{ lo, hi int }

type segRes struct {
	out                   []byte
	runs                  []span.Run
	cr                    bool
	crOff, wsStart, wsLen int
	firstOrig             int // OrigLo of the first run, -1 when nothing emitted
	err                   *norm.Error
}

func NormalizeK(buf []byte, k int, o norm.Options) ([]byte, *span.Map, error) {
	if k < 1 {
		k = 1
	}
	var cuts []int
	for i := 1; i < k; i++ {
		cuts = append(cuts, len(buf)*i/k)
	}
	return Normalize(buf, cuts, o)
}

func Normalize(buf []byte, cuts []int, o norm.Options) ([]byte, *span.Map, error) {
	pts := append(append([]int{0}, cuts...), len(buf))
	var segs []seg
	for i := 1; i < len(pts); i++ {
		if pts[i] < pts[i-1] {
			return nil, nil, ErrCuts
		}
		if pts[i] > pts[i-1] {
			segs = append(segs, seg{pts[i-1], pts[i]})
		}
	}
	res := make([]segRes, len(segs))
	var wg sync.WaitGroup
	for j, s := range segs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[j] = runSeg(buf, s, o)
		}()
	}
	wg.Wait()
	var first *norm.Error
	for _, r := range res {
		if r.err != nil && (first == nil || r.err.Off < first.Off) {
			first = r.err
		}
	}
	if first != nil {
		return nil, nil, first
	}
	return merge(buf, segs, res, o)
}
func runSeg(buf []byte, s seg, o norm.Options) segRes {
	o.Base, o.Policy = s.lo, norm.Keep
	n := norm.New(o)
	r := segRes{firstOrig: -1}
	if _, err := n.Write(buf[s.lo:s.hi]); err != nil {
		r.err = err.(*norm.Error)
		return r
	}
	r.out = n.Output()
	r.runs = n.Map().Runs()
	if len(r.runs) > 0 {
		r.firstOrig = r.runs[0].OrigLo
	}
	r.cr, r.crOff, r.wsStart, r.wsLen = n.Pending()
	return r
}
func merge(buf []byte, segs []seg, res []segRes, o norm.Options) ([]byte, *span.Map, error) {
	var gb span.Builder
	var out []byte
	emit := func(c byte, orig int) {
		gb.Keep(orig, orig+1, len(out))
		out = append(out, c)
	}
	var cOn, cCR bool // carried undecided tail from previous segments
	var cOff, cLen int
	flushWS := func() {
		for k := 0; k < cLen; k++ {
			emit(buf[cOff+k], cOff+k)
		}
	}
	for j, s := range segs {
		r := res[j]
		consumed := false
		if cOn && cCR {
			if buf[s.lo] != '\n' { // lone CR; absorbed CR is just deleted
				emit('\n', cOff)
			}
			cOn = false
		}
		if cOn { // carried whitespace run [cOff, cOff+cLen)
			switch b0 := buf[s.lo]; {
			case b0 == '\r' || b0 == '\n':
				cOn = false // trailing: deleted
			case b0 == ' ' || b0 == '\t':
				switch {
				case r.wsLen > 0 && r.wsStart == s.lo: // whole segment is ws: merge
					cLen += r.wsLen
					consumed = true
					if o.MaxWS > 0 && cLen > o.MaxWS {
						return nil, nil, &norm.Error{Kind: norm.KindWS, Off: cOff + o.MaxWS}
					}
				case r.firstOrig == s.lo: // leading ws became content
					flushWS()
					cOn = false
				default: // leading ws was deleted
					cOn = false
				}
			default:
				flushWS()
				cOn = false
			}
		}
		base := len(out)
		for _, rn := range r.runs {
			gb.Keep(rn.OrigLo, rn.OrigHi, base+rn.OutLo)
		}
		out = append(out, r.out...)
		if r.cr {
			cOn, cCR, cOff = true, true, r.crOff
		} else if r.wsLen > 0 && !consumed {
			cOn, cCR, cOff, cLen = true, false, r.wsStart, r.wsLen
		}
	}
	if cOn && cCR {
		emit('\n', cOff) // lone CR at end of stream
	}
	out = norm.Finalize(out, &gb, o.Policy)
	if o.MaxOut > 0 && len(out) > o.MaxOut {
		return nil, nil, &norm.Error{Kind: norm.KindOut, Off: len(buf)}
	}
	return out, gb.Build(len(buf)), nil
}
