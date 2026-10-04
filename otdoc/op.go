package otdoc

import "unicode/utf8"

// OpKind identifies the kind of an operation component.
type OpKind int

const (
	OpRetain OpKind = iota
	OpInsert
	OpDelete
)

// Component is a single operation component. Retain and Delete carry a rune
// count N (>= 1); Insert carries a non-empty valid UTF-8 string S.
type Component struct {
	Kind OpKind
	N    int
	S    string
}

// Retain builds a Retain(n) component.
func Retain(n int) Component { return Component{Kind: OpRetain, N: n} }

// Insert builds an Insert(s) component.
func Insert(s string) Component { return Component{Kind: OpInsert, S: s} }

// Delete builds a Delete(n) component.
func Delete(n int) Component { return Component{Kind: OpDelete, N: n} }

// Operation is a sequence of components. Its base length is the sum of all
// Retain and Delete counts and must equal the document length (in runes) at
// its base revision.
type Operation []Component

// Normalize returns the canonical form of op: adjacent same-kind components
// are merged and adjacent Insert/Delete pairs are ordered Insert first.
func Normalize(op Operation) Operation {
	merged := mergeAdjacent(op)
	for {
		swapped := false
		for i := 0; i+1 < len(merged); i++ {
			if merged[i].Kind == OpDelete && merged[i+1].Kind == OpInsert {
				merged[i], merged[i+1] = merged[i+1], merged[i]
				swapped = true
			}
		}
		if !swapped {
			return merged
		}
		merged = mergeAdjacent(merged)
	}
}

func mergeAdjacent(op Operation) Operation {
	var out Operation
	for _, c := range op {
		if len(out) > 0 && out[len(out)-1].Kind == c.Kind {
			last := &out[len(out)-1]
			if c.Kind == OpInsert {
				last.S += c.S
			} else {
				last.N += c.N
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

func validate(op Operation) error {
	if len(op) == 0 {
		return ErrInvalid
	}
	for _, c := range op {
		switch c.Kind {
		case OpRetain, OpDelete:
			if c.N < 1 {
				return ErrInvalid
			}
		case OpInsert:
			if c.S == "" || !utf8.ValidString(c.S) {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

func baseLen(op Operation) int {
	n := 0
	for _, c := range op {
		if c.Kind == OpRetain || c.Kind == OpDelete {
			n += c.N
		}
	}
	return n
}

func insertRunes(op Operation) int {
	n := 0
	for _, c := range op {
		if c.Kind == OpInsert {
			n += utf8.RuneCountInString(c.S)
		}
	}
	return n
}

func deleteRunes(op Operation) int {
	n := 0
	for _, c := range op {
		if c.Kind == OpDelete {
			n += c.N
		}
	}
	return n
}

func isNoop(op Operation) bool {
	for _, c := range op {
		if c.Kind != OpRetain {
			return false
		}
	}
	return true
}

func apply(doc []rune, op Operation) []rune {
	var out []rune
	pos := 0
	for _, c := range op {
		switch c.Kind {
		case OpRetain:
			out = append(out, doc[pos:pos+c.N]...)
			pos += c.N
		case OpDelete:
			pos += c.N
		case OpInsert:
			out = append(out, []rune(c.S)...)
		}
	}
	return out
}

func cloneOp(op Operation) Operation {
	if op == nil {
		return nil
	}
	out := make(Operation, len(op))
	copy(out, op)
	return out
}
