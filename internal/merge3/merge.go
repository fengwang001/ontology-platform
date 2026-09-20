package merge3

import "slices"

// Conflict describes one minimal conflicting region.
type Conflict struct {
	// BaseStart and BaseEnd give the half-open line range in base.
	BaseStart int
	BaseEnd   int
	// Line is the 0-based start line of the conflict in Result.Lines.
	Line int

	Base   []string
	Ours   []string
	Theirs []string
}

// Result is the outcome of Merge.
type Result struct {
	// Lines is the merged content. Conflicting regions contribute their
	// Ours lines; use Render for diff3-style marker output.
	Lines     []string
	Conflicts []Conflict

	segments []segment
}

// HasConflict reports whether the merge produced any conflict.
func (r Result) HasConflict() bool { return len(r.Conflicts) > 0 }

// segment is a piece of the merged output: either clean lines or a conflict.
type segment struct {
	lines    []string
	conflict int // index into Result.Conflicts, -1 for clean segments
}

// Merge performs a line-based three-way merge of ours and theirs against
// their common ancestor base.
func Merge(base, ours, theirs []string) (Result, error) {
	oursChanges := diff(base, ours)
	theirChanges := diff(base, theirs)

	var r Result
	var clean []string
	flushClean := func() {
		if len(clean) > 0 {
			r.segments = append(r.segments, segment{lines: clean, conflict: -1})
			clean = nil
		}
	}
	emit := func(lines []string) {
		clean = append(clean, lines...)
		r.Lines = append(r.Lines, lines...)
	}
	emitConflict := func(c Conflict) {
		flushClean()
		c.Line = len(r.Lines)
		r.Conflicts = append(r.Conflicts, c)
		r.segments = append(r.segments, segment{conflict: len(r.Conflicts) - 1})
		r.Lines = append(r.Lines, c.Ours...)
	}

	pos := 0 // current base line
	i, j := 0, 0
	for i < len(oursChanges) || j < len(theirChanges) {
		// Seed a region with the earliest remaining change.
		var rs, re int
		var ocs, tcs []Change
		takeOurs := i < len(oursChanges) &&
			(j >= len(theirChanges) || oursChanges[i].Start <= theirChanges[j].Start)
		if takeOurs {
			rs, re = oursChanges[i].Start, oursChanges[i].End
			ocs = append(ocs, oursChanges[i])
			i++
		} else {
			rs, re = theirChanges[j].Start, theirChanges[j].End
			tcs = append(tcs, theirChanges[j])
			j++
		}
		// Grow the region while either side has changes overlapping it.
		for {
			grown := false
			for i < len(oursChanges) && overlaps(oursChanges[i], rs, re) {
				ocs = append(ocs, oursChanges[i])
				re = max(re, oursChanges[i].End)
				i++
				grown = true
			}
			for j < len(theirChanges) && overlaps(theirChanges[j], rs, re) {
				tcs = append(tcs, theirChanges[j])
				re = max(re, theirChanges[j].End)
				j++
				grown = true
			}
			if !grown {
				break
			}
		}

		emit(base[pos:rs])
		pos = re

		baseLines := slices.Clone(base[rs:re])
		ourLines := applyChanges(base, rs, re, ocs)
		theirLines := applyChanges(base, rs, re, tcs)
		switch {
		case slices.Equal(ourLines, baseLines):
			emit(theirLines) // only theirs changed
		case slices.Equal(theirLines, baseLines),
			slices.Equal(ourLines, theirLines):
			emit(ourLines) // only ours changed, or both changed identically
		default:
			c := Conflict{
				BaseStart: rs, BaseEnd: re,
				Base: baseLines, Ours: ourLines, Theirs: theirLines,
			}
			if c, suffix, ok := minimize(c, emit); ok {
				emitConflict(c)
				emit(suffix)
			}
		}
	}
	emit(base[pos:])
	flushClean()
	return r, nil
}

func overlaps(c Change, rs, re int) bool {
	if c.Start < re && rs < c.End {
		return true
	}
	// Two pure insertions at the same anchor point overlap.
	return c.Start == c.End && rs == re && c.Start == rs
}

// applyChanges replays the changes of one side inside base[rs:re].
func applyChanges(base []string, rs, re int, cs []Change) []string {
	var out []string
	pos := rs
	for _, c := range cs {
		out = append(out, base[pos:c.Start]...)
		out = append(out, c.Lines...)
		pos = c.End
	}
	return append(out, base[pos:re]...)
}

// minimize strips common leading and trailing lines from Ours and Theirs so
// the conflict covers only the truly divergent lines. Stripped prefix lines
// are emitted at once via emit; stripped suffix lines are returned for the
// caller to emit after the conflict. It reports whether a conflict remains.
func minimize(c Conflict, emit func([]string)) (Conflict, []string, bool) {
	for len(c.Ours) > 0 && len(c.Theirs) > 0 && c.Ours[0] == c.Theirs[0] {
		emit(c.Ours[:1])
		if len(c.Base) > 0 && c.Base[0] == c.Ours[0] {
			c.Base = c.Base[1:]
			c.BaseStart++
		}
		c.Ours = c.Ours[1:]
		c.Theirs = c.Theirs[1:]
	}
	var suffix []string
	for len(c.Ours) > 0 && len(c.Theirs) > 0 &&
		c.Ours[len(c.Ours)-1] == c.Theirs[len(c.Theirs)-1] {
		last := c.Ours[len(c.Ours)-1]
		if n := len(c.Base); n > 0 && c.Base[n-1] == last {
			c.Base = c.Base[:n-1]
			c.BaseEnd--
		}
		suffix = append([]string{last}, suffix...)
		c.Ours = c.Ours[:len(c.Ours)-1]
		c.Theirs = c.Theirs[:len(c.Theirs)-1]
	}
	if slices.Equal(c.Ours, c.Theirs) {
		emit(c.Ours)
		emit(suffix)
		return Conflict{}, nil, false
	}
	return c, suffix, true
}
