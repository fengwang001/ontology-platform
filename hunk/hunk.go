// Package hunk groups an edit script into hunks with C context lines,
// merging changes separated by at most 2C unchanged lines (see DESIGN.md).
package hunk

import "ontology/edit"

// Hunk is a run of edit ops covering one changed region plus context.
// AStart/BStart are 0-based start indices into the old/new line sequences.
type Hunk struct {
	Ops            []edit.Op
	AStart, BStart int
	ACount, BCount int
}

// Group splits script into hunks with up to context unchanged lines on
// each side. Two changes with g unchanged lines between them merge into
// one hunk iff g <= 2*context.
func Group(script []edit.Op, context int) []Hunk {
	var ch []int // script indices of change ops
	for i, op := range script {
		if op.Kind != ' ' {
			ch = append(ch, i)
		}
	}
	if len(ch) == 0 {
		return nil
	}
	var hs []Hunk
	lo := ch[0]
	for i := 1; i <= len(ch); i++ {
		if i < len(ch) && ch[i]-ch[i-1]-1 <= 2*context {
			continue
		}
		hi := ch[i-1]
		from := lo - context
		if from < 0 {
			from = 0
		}
		to := hi + context + 1
		if to > len(script) {
			to = len(script)
		}
		hs = append(hs, newHunk(script[from:to]))
		if i < len(ch) {
			lo = ch[i]
		}
	}
	return hs
}

func newHunk(ops []edit.Op) Hunk {
	h := Hunk{Ops: ops, AStart: ops[0].A, BStart: ops[0].B}
	for _, op := range ops {
		switch op.Kind {
		case ' ':
			h.ACount++
			h.BCount++
		case '-':
			h.ACount++
		case '+':
			h.BCount++
		}
	}
	return h
}
