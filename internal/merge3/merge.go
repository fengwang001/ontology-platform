package merge3

import "slices"

// Conflict describes one minimal conflicting region of a three-way merge.
type Conflict struct {
	// LineIndex is the number of cleanly merged lines (Result.Lines) that
	// precede this conflict, i.e. where the conflict sits in the output.
	LineIndex int
	Base      []string
	Ours      []string
	Theirs    []string
}

// Result is the outcome of Merge.
type Result struct {
	// Lines holds the cleanly merged lines in order. Conflicting regions
	// are not included; see Conflicts for their content and position.
	Lines     []string
	Conflicts []Conflict
}

// HasConflict reports whether the merge produced any conflict.
func (r Result) HasConflict() bool { return len(r.Conflicts) > 0 }

// chunk is an ordered piece of the merge output: either clean lines or a
// single conflict.
type chunk struct {
	lines    []string
	conflict *Conflict
}

// Merge performs a line-based three-way merge of ours and theirs against
// their common ancestor base. Inputs are lines without trailing newlines.
func Merge(base, ours, theirs []string) (Result, error) {
	matchOurs := lcsMatches(base, ours)
	matchTheirs := lcsMatches(base, theirs)

	var chunks []chunk
	bi, oi, ti := 0, 0, 0
	// Base lines matched on both sides are stable anchors. Everything
	// between two anchors is one changed region to resolve.
	for b := 0; b < len(base); b++ {
		if matchOurs[b] < 0 || matchTheirs[b] < 0 {
			continue
		}
		chunks = append(chunks, resolveRegion(base[bi:b], ours[oi:matchOurs[b]], theirs[ti:matchTheirs[b]])...)
		chunks = append(chunks, chunk{lines: []string{base[b]}})
		bi, oi, ti = b+1, matchOurs[b]+1, matchTheirs[b]+1
	}
	chunks = append(chunks, resolveRegion(base[bi:], ours[oi:], theirs[ti:])...)

	var res Result
	for _, c := range chunks {
		if c.conflict != nil {
			c.conflict.LineIndex = len(res.Lines)
			res.Conflicts = append(res.Conflicts, *c.conflict)
			continue
		}
		res.Lines = append(res.Lines, c.lines...)
	}
	return res, nil
}

// resolveRegion merges one changed region: the base slice and the
// corresponding ours/theirs slices between two anchors.
func resolveRegion(b, o, t []string) []chunk {
	switch {
	case slices.Equal(o, b):
		return cleanChunks(t)
	case slices.Equal(t, b):
		return cleanChunks(o)
	case slices.Equal(o, t):
		return cleanChunks(o)
	}

	// Strip leading and trailing lines that ours and theirs agree on; only
	// the truly divergent middle may become a conflict.
	pre, b, o, t := stripCommonPrefix(b, o, t)
	suf, b, o, t := stripCommonSuffix(b, o, t)

	var mid []chunk
	switch {
	case slices.Equal(o, b):
		mid = cleanChunks(t)
	case slices.Equal(t, b):
		mid = cleanChunks(o)
	case slices.Equal(o, t):
		mid = cleanChunks(o)
	default:
		mid = []chunk{{conflict: &Conflict{
			Base:   slices.Clone(b),
			Ours:   slices.Clone(o),
			Theirs: slices.Clone(t),
		}}}
	}

	out := make([]chunk, 0, 3)
	out = append(out, cleanChunks(pre)...)
	out = append(out, mid...)
	out = append(out, cleanChunks(suf)...)
	return out
}

// stripCommonPrefix removes leading lines shared by ours and theirs,
// returning them along with the remaining slices. Matching base lines are
// dropped as well so the conflict keeps only divergent base content.
func stripCommonPrefix(b, o, t []string) (pre, rb, ro, rt []string) {
	for len(o) > 0 && len(t) > 0 && o[0] == t[0] {
		pre = append(pre, o[0])
		if len(b) > 0 && b[0] == o[0] {
			b = b[1:]
		}
		o, t = o[1:], t[1:]
	}
	return pre, b, o, t
}

// stripCommonSuffix mirrors stripCommonPrefix for trailing lines.
func stripCommonSuffix(b, o, t []string) (suf, rb, ro, rt []string) {
	for len(o) > 0 && len(t) > 0 && o[len(o)-1] == t[len(t)-1] {
		suf = append(suf, o[len(o)-1])
		if len(b) > 0 && b[len(b)-1] == o[len(o)-1] {
			b = b[:len(b)-1]
		}
		o, t = o[:len(o)-1], t[:len(t)-1]
	}
	slices.Reverse(suf)
	return suf, b, o, t
}

func cleanChunks(lines []string) []chunk {
	if len(lines) == 0 {
		return nil
	}
	return []chunk{{lines: lines}}
}
