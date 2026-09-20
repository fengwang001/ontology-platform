package merge3

// Conflict describes one minimal conflicting region of a merge.
type Conflict struct {
	Line      int      // index into Result.Lines where the conflict belongs
	BaseStart int      // start line in base (0-based, inclusive)
	BaseEnd   int      // end line in base (0-based, exclusive)
	Ours      []string // our side's lines for the region
	Base      []string // base lines for the region
	Theirs    []string // their side's lines for the region
}

// Result is the outcome of Merge. Lines holds the cleanly merged
// lines only; conflicting regions are not spliced in but described
// by Conflicts (use Render for a full diff3-style text).
type Result struct {
	Lines     []string
	Conflicts []Conflict
}

// HasConflicts reports whether the merge produced any conflict.
func (r Result) HasConflicts() bool { return len(r.Conflicts) > 0 }

// Merge performs a line-level three-way merge of ours and theirs
// against their common ancestor base.
func Merge(base, ours, theirs []string) (Result, error) {
	oh := diff(base, ours)
	th := diff(base, theirs)
	r := Result{Lines: []string{}}
	pos := 0
	i, j := 0, 0
	for i < len(oh) || j < len(th) {
		g1, g2, og, tg, ni, nj := nextGroup(oh, th, i, j)
		i, j = ni, nj
		r.Lines = append(r.Lines, base[pos:g1]...)
		oSlice := applyHunks(base, g1, g2, og)
		tSlice := applyHunks(base, g1, g2, tg)
		bSlice := base[g1:g2]
		r.resolve(oSlice, bSlice, tSlice, g1, g2)
		pos = g2
	}
	r.Lines = append(r.Lines, base[pos:]...)
	return r, nil
}

// resolve appends the merged outcome of one region to r.
func (r *Result) resolve(o, b, t []string, g1, g2 int) {
	switch {
	case equalLines(o, t):
		r.Lines = append(r.Lines, o...) // identical on both sides
	case equalLines(o, b):
		r.Lines = append(r.Lines, t...) // only theirs changed
	case equalLines(t, b):
		r.Lines = append(r.Lines, o...) // only ours changed
	default:
		r.addConflict(o, b, t, g1, g2)
	}
}

// addConflict strips the common head/tail lines shared by ours and
// theirs so only the truly divergent middle forms the conflict.
func (r *Result) addConflict(o, b, t []string, g1, g2 int) {
	p := commonPrefix(o, t)
	s := commonSuffix(o[p:], t[p:])
	r.Lines = append(r.Lines, o[:p]...)
	oMid := o[p : len(o)-s]
	tMid := t[p : len(t)-s]
	// Trim base lines that agree with the stripped affixes so the
	// recorded base section stays minimal as well.
	bp := commonPrefix(b, o[:p])
	bMid := b[bp:]
	bs := commonSuffix(bMid, o[len(o)-s:])
	bMid = bMid[:len(bMid)-bs]
	r.Conflicts = append(r.Conflicts, Conflict{
		Line:      len(r.Lines),
		BaseStart: g1 + bp,
		BaseEnd:   g2 - bs,
		Ours:      oMid,
		Base:      bMid,
		Theirs:    tMid,
	})
	// The shared tail merges cleanly and follows the conflict block.
	r.Lines = append(r.Lines, o[len(o)-s:]...)
}

// nextGroup pulls out the next connected component of overlapping
// hunks from both sides and returns its base range [g1, g2).
func nextGroup(oh, th []hunk, i, j int) (g1, g2 int, og, tg []hunk, ni, nj int) {
	// Seed with the hunk that starts first; at equal starts an
	// insertion (empty range) goes before a replacement.
	if j >= len(th) || (i < len(oh) && lessHunk(oh[i], th[j])) {
		g1, g2 = oh[i].start, oh[i].end
		og = append(og, oh[i])
		i++
	} else {
		g1, g2 = th[j].start, th[j].end
		tg = append(tg, th[j])
		j++
	}
	for {
		grown := false
		for i < len(oh) && overlaps(oh[i], g1, g2) {
			og = append(og, oh[i])
			g1, g2 = min(g1, oh[i].start), max(g2, oh[i].end)
			i++
			grown = true
		}
		for j < len(th) && overlaps(th[j], g1, g2) {
			tg = append(tg, th[j])
			g1, g2 = min(g1, th[j].start), max(g2, th[j].end)
			j++
			grown = true
		}
		if !grown {
			break
		}
	}
	return g1, g2, og, tg, i, j
}

// lessHunk orders hunks by start, with insertions before replacements.
func lessHunk(a, b hunk) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return a.end-a.start < b.end-b.start
}

// overlaps reports whether h touches the group range [g1, g2).
// Insertions (empty ranges) only join a group when they sit strictly
// inside it or at exactly the same empty point.
func overlaps(h hunk, g1, g2 int) bool {
	if h.start == h.end {
		if g1 == g2 {
			return h.start == g1
		}
		return g1 < h.start && h.start < g2
	}
	if g1 == g2 {
		return h.start < g1 && g1 < h.end
	}
	return h.start < g2 && g1 < h.end
}

// applyHunks reconstructs one side's lines over base[g1:g2].
func applyHunks(base []string, g1, g2 int, hs []hunk) []string {
	var out []string
	pos := g1
	for _, h := range hs {
		out = append(out, base[pos:h.start]...)
		out = append(out, h.lines...)
		pos = h.end
	}
	return append(out, base[pos:g2]...)
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}
