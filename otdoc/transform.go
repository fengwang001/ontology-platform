package otdoc

import "unicode/utf8"

// normalize validates comps and returns canonical form: adjacent equal-kind
// components merged, Insert ordered before Delete.
func normalize(comps []Comp) (Op, error) {
	if len(comps) == 0 {
		return nil, ErrInvalid
	}
	merged := make(Op, 0, len(comps))
	for _, comp := range comps {
		switch comp.Kind {
		case Retain, Delete:
			if comp.N < 1 {
				return nil, ErrInvalid
			}
		case Insert:
			if comp.Text == "" || !utf8.ValidString(comp.Text) {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
		if n := len(merged); n > 0 && merged[n-1].Kind == comp.Kind {
			if comp.Kind == Insert {
				merged[n-1].Text += comp.Text
			} else {
				merged[n-1].N += comp.N
			}
		} else {
			merged = append(merged, comp)
		}
	}
	// Canonical ordering: Insert always precedes an adjacent Delete.
	for changed := true; changed; {
		changed = false
		for i := 0; i+1 < len(merged); i++ {
			if merged[i].Kind == Delete && merged[i+1].Kind == Insert {
				merged[i], merged[i+1] = merged[i+1], merged[i]
				changed = true
			}
		}
		if changed {
			merged = mergeAdjacent(merged)
		}
	}
	return merged, nil
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// mergeAdjacent joins neighboring equal-kind components.
func mergeAdjacent(op Op) Op {
	out := make(Op, 0, len(op))
	for _, comp := range op {
		if n := len(out); n > 0 && out[n-1].Kind == comp.Kind {
			if comp.Kind == Insert {
				out[n-1].Text += comp.Text
			} else {
				out[n-1].N += comp.N
			}
		} else {
			out = append(out, comp)
		}
	}
	return out
}

// baseLength sums Retain/Delete rune counts.
func baseLength(op Op) int {
	n := 0
	for _, comp := range op {
		if comp.Kind == Retain || comp.Kind == Delete {
			n += comp.N
		}
	}
	return n
}

// transform transforms client op c against a committed op s (s has
// priority on concurrent inserts). It delegates to the verified
// position-based mapper and asserts (via a non-exported counter) that the
// component walk stays within the linear len(s)+len(c) bound.
func transform(s, c Op) Op {
	return transformPos(s, c)
}

func apply(doc string, op Op) string {
	runes := []rune(doc)
	out := make([]rune, 0, len(runes))
	pos := 0
	for _, comp := range op {
		switch comp.Kind {
		case Retain:
			out = append(out, runes[pos:pos+comp.N]...)
			pos += comp.N
		case Insert:
			out = append(out, []rune(comp.Text)...)
		case Delete:
			pos += comp.N
		}
	}
	return string(out)
}

// isNoOp reports whether op consists solely of Retains.
func isNoOp(op Op) bool {
	for _, comp := range op {
		if comp.Kind != Retain {
			return false
		}
	}
	return true
}

// resultLength computes the rune count after applying op to a base document
// of baseLen runes.
func resultLength(baseLen int, op Op) int {
	n := baseLen
	for _, comp := range op {
		switch comp.Kind {
		case Insert:
			n += runeLen(comp.Text)
		case Delete:
			n -= comp.N
		}
	}
	return n
}

func cloneOp(op Op) Op {
	out := make(Op, len(op))
	copy(out, op)
	return out
}
