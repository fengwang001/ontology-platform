// Package ivl holds the closed integer-interval set for one GTID source.
package ivl

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Interval is a closed interval [Lo, Hi]; Lo <= Hi always.
type Interval struct {
	Lo, Hi int64
}

// Set is a normalized (sorted, disjoint, non-adjacent) interval set.
// probes counts executed intervals read by the latest subtraction
// (each binary-search probe included); it is unexported on purpose and
// is only meaningful on the receiver the subtraction ran on.
type Set struct {
	iv     []Interval
	probes int
}

// normalize returns the canonical form of ivs: sorted by Lo, with
// overlapping or adjacent (Hi+1 == next Lo) intervals merged.
func normalize(ivs []Interval) []Interval {
	cp := make([]Interval, len(ivs))
	copy(cp, ivs)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Lo < cp[j].Lo })
	out := cp[:0]
	for _, v := range cp {
		if n := len(out); n > 0 {
			last := &out[n-1]
			// Guard against Hi+1 overflow at math.MaxInt64.
			if v.Lo <= last.Hi || (last.Hi < math.MaxInt64 && v.Lo == last.Hi+1) {
				if v.Hi > last.Hi {
					last.Hi = v.Hi
				}
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

// NewSet copies ivs and returns its normalized form.
func NewSet(ivs []Interval) Set { return Set{iv: normalize(ivs)} }

// Len reports the number of normalized intervals.
func (s Set) Len() int { return len(s.iv) }

// Union returns the normalized union of s and o.
func (s Set) Union(o Set) Set {
	both := make([]Interval, 0, len(s.iv)+len(o.iv))
	both = append(both, s.iv...)
	both = append(both, o.iv...)
	return Set{iv: normalize(both)}
}

// Subtract returns src minus e (e is the executed set). It locates each
// source interval with a binary search, then walks only the executed
// intervals it actually intersects; every interval read (including each
// binary-search probe) is tallied in e.probes.
func (e *Set) Subtract(src Set) Set {
	e.probes = 0
	var out []Interval
	for _, q := range src.iv {
		s, en := q.Lo, q.Hi
		// First executed index whose Hi >= s.
		lo, hi := 0, len(e.iv)
		for lo < hi {
			mid := lo + (hi-lo)/2
			e.probes++
			if e.iv[mid].Hi >= s {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		j := lo
		emitTail := true
		for j < len(e.iv) {
			a, b := e.iv[j].Lo, e.iv[j].Hi
			e.probes++
			if a > en { // this and all later intervals lie past the source
				break
			}
			if s < a { // gap before this executed interval
				out = append(out, Interval{s, a - 1})
			}
			if b >= en { // executed interval reaches the source end
				emitTail = false
				break
			}
			s = b + 1 // b < en <= MaxInt64, so no overflow
			j++
		}
		if emitTail && s <= en {
			out = append(out, Interval{s, en})
		}
	}
	return Set{iv: out} // out is already normalized
}

// Format renders the set as n or a-b joined by ':'.
func (s Set) Format() string {
	var b strings.Builder
	for i, v := range s.iv {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(strconv.FormatInt(v.Lo, 10))
		if v.Hi != v.Lo {
			b.WriteByte('-')
			b.WriteString(strconv.FormatInt(v.Hi, 10))
		}
	}
	return b.String()
}
