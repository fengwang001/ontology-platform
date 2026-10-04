// Package edit maps an edit list to a retimed cue set.
package edit

import (
	"errors"

	"ontology/cue"
)

var (
	ErrInvalidParam = errors.New("edit: invalid parameter")
)

type Deletion struct {
	A int64
	B int64
}

type Insertion struct {
	At  int64
	Len int64
}

type EditList struct {
	Deletions  []Deletion
	Insertions []Insertion
}

type Result struct {
	Cues    []cue.Cue
	Splits  int
	Dropped int
}

const (
	maxItems = 10_000
	maxCoord = cue.MaxTime
)

// retimer holds prefix sums of a validated edit list. probes counts the
// number of edit-list items compared by binary searches during Retime.
type retimer struct {
	dels   []Deletion
	delSum []int64
	delA   []int64
	inss   []Insertion
	insSum []int64
	insAt  []int64
	probes int
}

func newRetimer(e EditList) *retimer {
	r := &retimer{dels: e.Deletions, inss: e.Insertions}
	r.delSum = make([]int64, len(r.dels)+1)
	r.delA = make([]int64, len(r.dels))
	for i, d := range r.dels {
		r.delSum[i+1] = r.delSum[i] + (d.B - d.A)
		r.delA[i] = d.A
	}
	r.insSum = make([]int64, len(r.inss)+1)
	r.insAt = make([]int64, len(r.inss))
	for i, in := range r.inss {
		r.insSum[i+1] = r.insSum[i] + in.Len
		r.insAt[i] = in.At
	}
	return r
}

// bisectLeft returns the first index i with v >= vs[i], counting every
// comparison against an edit-list item.
func (r *retimer) bisectLeft(vs []int64, v int64) int {
	lo, hi := 0, len(vs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		r.probes++
		if vs[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// del returns total deleted length intersecting [0, t).
func (r *retimer) del(t int64) int64 {
	if t <= 0 {
		return 0
	}
	i := r.bisectLeft(r.delA, t)
	if i == 0 {
		return 0
	}
	d := r.dels[i-1]
	if d.B > t {
		return r.delSum[i-1] + (t - d.A)
	}
	return r.delSum[i]
}

func (r *retimer) insL(t int64) int64 {
	return r.insSum[r.bisectLeft(r.insAt, t)]
}

func (r *retimer) insR(t int64) int64 {
	return r.insSum[r.bisectLeft(r.insAt, t+1)]
}

func (r *retimer) fL(t int64) int64 { return t - r.del(t) + r.insL(t) }
func (r *retimer) fR(t int64) int64 { return t - r.del(t) + r.insR(t) }

// internalInsertions returns insertion points strictly inside (s, e),
// touching each reported item plus O(log k) binary-search items.
func (r *retimer) internalInsertions(s, e int64) []Insertion {
	i := r.bisectLeft(r.insAt, s+1) // first at > s
	j := i
	for j < len(r.inss) && r.inss[j].At < e {
		j++
	}
	if j == i {
		return nil
	}
	return r.inss[i:j]
}

func withinCoord(t int64) bool { return 0 <= t && t <= maxCoord }

func Validate(e EditList) error {
	if len(e.Deletions) > maxItems || len(e.Insertions) > maxItems {
		return ErrInvalidParam
	}
	for i, d := range e.Deletions {
		if !withinCoord(d.A) || !withinCoord(d.B) || d.A >= d.B {
			return ErrInvalidParam
		}
		if i > 0 {
			prev := e.Deletions[i-1]
			if d.A < prev.A || d.A < prev.B {
				return ErrInvalidParam
			}
		}
	}
	for i, in := range e.Insertions {
		if !withinCoord(in.At) || in.Len < 1 {
			return ErrInvalidParam
		}
		if i > 0 && in.At <= e.Insertions[i-1].At {
			return ErrInvalidParam
		}
	}
	// Insertion points must not lie strictly inside a deletion.
	if len(e.Deletions) > 0 {
		r0 := newRetimer(e)
		for _, in := range e.Insertions {
			i := sortInts(r0.delA, in.At) // first a >= at
			if i > 0 && in.At > e.Deletions[i-1].A && in.At < e.Deletions[i-1].B {
				return ErrInvalidParam
			}
		}
	}
	return nil
}

// sortInts is a probe-free sort.SearchInts used only for validation.
func sortInts(vs []int64, v int64) int {
	lo, hi := 0, len(vs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if vs[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Retime maps every cue through the edit list. It assumes cues are valid;
// the track layer is responsible for cue validation and rejection order.
func Retime(dmin int64, e EditList, cues []cue.Cue) (Result, error) {
	if dmin < cue.MinDmin || dmin > cue.MaxDmin {
		return Result{}, ErrInvalidParam
	}
	if err := Validate(e); err != nil {
		return Result{}, err
	}
	r := newRetimer(e)
	res := Result{Cues: make([]cue.Cue, 0, len(cues))}
	for _, c := range cues {
		pts := make([]int64, 0, 2)
		pts = append(pts, c.Start)
		for _, in := range r.internalInsertions(c.Start, c.End) {
			pts = append(pts, in.At)
			res.Splits++
		}
		pts = append(pts, c.End)
		for i := 0; i+1 < len(pts); i++ {
			p, q := pts[i], pts[i+1]
			ns, ne := r.fR(p), r.fL(q)
			if ne-ns < dmin {
				res.Dropped++
				continue
			}
			res.Cues = append(res.Cues, cue.Cue{Start: ns, End: ne, Text: c.Text})
		}
	}
	return res, nil
}
