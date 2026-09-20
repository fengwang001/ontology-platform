package merge3

// Conflict describes one minimal conflicting region of a merge.
type Conflict struct {
	// Start is the index into Result.Lines where the conflicting
	// ours-side lines begin.
	Start int
	// BaseStart is the index into the original base lines where the
	// conflicting base lines begin.
	BaseStart int
	// Ours, Base and Theirs hold the three sides of the conflict.
	// Any of them may be empty (e.g. a deletion).
	Ours   []string
	Base   []string
	Theirs []string
}

// Result is the outcome of Merge. Lines holds the merged lines; for
// conflicted regions it contains the ours-side lines, and Conflicts
// records where each conflict sits and what all three sides contain.
type Result struct {
	Lines     []string
	Conflicts []Conflict
}

// HasConflicts reports whether the merge produced any conflict.
func (r Result) HasConflicts() bool { return len(r.Conflicts) > 0 }

func (r *Result) appendClean(lines []string) {
	r.Lines = append(r.Lines, lines...)
}

// appendConflict strips the common prefix/suffix shared by oursSeg and
// theirsSeg, emits those as clean lines, and records a minimal conflict
// for the diverging middle.
func (r *Result) appendConflict(baseStart int, baseSeg, oursSeg, theirsSeg []string) {
	pre := commonPrefix(oursSeg, theirsSeg)
	suf := commonSuffix(oursSeg[pre:], theirsSeg[pre:])
	oMid := oursSeg[pre : len(oursSeg)-suf]
	tMid := theirsSeg[pre : len(theirsSeg)-suf]
	// Strip from the base section only what genuinely matches the
	// stripped affixes, so the base view stays accurate.
	bpre := commonPrefix(baseSeg, oursSeg[:pre])
	bsuf := commonSuffix(baseSeg[bpre:], oursSeg[len(oursSeg)-suf:])
	bMid := baseSeg[bpre : len(baseSeg)-bsuf]

	r.appendClean(oursSeg[:pre])
	r.Conflicts = append(r.Conflicts, Conflict{
		Start:     len(r.Lines),
		BaseStart: baseStart + bpre,
		Ours:      oMid,
		Base:      bMid,
		Theirs:    tMid,
	})
	r.appendClean(oMid)
	r.appendClean(oursSeg[len(oursSeg)-suf:])
}

// Render formats the result in diff3 style. Lines belonging to
// conflicts are replaced by marker blocks. It returns ErrEmptyLabel
// if either label is empty.
func (r Result) Render(ourLabel, theirLabel string) ([]string, error) {
	if ourLabel == "" || theirLabel == "" {
		return nil, ErrEmptyLabel
	}
	out := make([]string, 0, len(r.Lines))
	pos := 0
	for _, c := range r.Conflicts {
		out = append(out, r.Lines[pos:c.Start]...)
		out = append(out, "<<<<<<< "+ourLabel)
		out = append(out, c.Ours...)
		out = append(out, "||||||| base")
		out = append(out, c.Base...)
		out = append(out, "=======")
		out = append(out, c.Theirs...)
		out = append(out, ">>>>>>> "+theirLabel)
		pos = c.Start + len(c.Ours)
	}
	out = append(out, r.Lines[pos:]...)
	return out, nil
}
