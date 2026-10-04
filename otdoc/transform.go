package otdoc

import "unicode/utf8"

// transform transforms client operation c against already-stored server
// operation s (s is ordered first). s must be canonical and both operations
// must share the same base length. The result is NOT normalized: only the
// submit input and the final outcome of a transform chain are normalized, so
// that transforming step by step is equivalent to mapping every character
// position of the base revision to the current revision. The returned
// component-processing step count never exceeds len(c)+len(s).
func transform(c, s Operation) (Operation, int) {
	var out Operation
	steps := 0
	i, j := 0, 0   // current component indices into s and c
	si, ci := 0, 0 // consumed runes inside the current s / c component
	for i < len(s) && j < len(c) {
		steps++
		sc, cc := s[i], c[j]
		switch {
		case sc.Kind == OpInsert:
			// Stored insert is ordered first: c retains over it.
			out = append(out, Retain(utf8.RuneCountInString(sc.S)))
			i++
			si = 0
		case cc.Kind == OpInsert:
			// Client insert is emitted as-is (also when it falls inside a
			// region deleted by s: it is kept at the deletion point).
			out = append(out, cc)
			j++
			ci = 0
		default:
			sn, cn := sc.N-si, cc.N-ci
			m := sn
			if cn < sn {
				m = cn
			}
			switch {
			case sc.Kind == OpRetain && cc.Kind == OpRetain:
				out = append(out, Retain(m))
			case sc.Kind == OpDelete && cc.Kind == OpRetain:
				// c's retain falls into a region s deleted: it vanishes.
			case sc.Kind == OpRetain && cc.Kind == OpDelete:
				out = append(out, Delete(m))
			case sc.Kind == OpDelete && cc.Kind == OpDelete:
				// Overlapping deletes are removed only once.
			}
			si += m
			ci += m
			if si == sc.N {
				i++
				si = 0
			}
			if ci == cc.N {
				j++
				ci = 0
			}
		}
	}
	// The remaining components of s can only be inserts (retain/delete runs
	// are consumed in lockstep with c); c retains over them.
	for ; i < len(s); i++ {
		steps++
		out = append(out, Retain(utf8.RuneCountInString(s[i].S)))
	}
	// The remaining components of c can only be inserts; emit them as-is.
	for ; j < len(c); j++ {
		steps++
		out = append(out, c[j])
	}
	return out, steps
}
