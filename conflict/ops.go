package conflict

import "strings"

// resolveLocked resolves the i-th (0-based) unresolved block.
//
// Rejection precedence: ErrNoSuchBlock > ErrNoBase > ErrBadLine >
// ErrTooLarge. A rejected resolve leaves the document unchanged.
func (d *Doc) resolveLocked(i int, choice Choice, custom []string) error {
	// Locate the i-th block.
	idx := -1
	seen := 0
	for j := range d.segs {
		if d.segs[j].isBlock {
			if seen == i {
				idx = j
				break
			}
			seen++
		}
	}
	if i < 0 || idx < 0 {
		return ErrNoSuchBlock
	}
	blk := &d.segs[idx]

	var repl []string
	switch choice {
	case Ours:
		repl = blk.ours
	case Theirs:
		repl = blk.theirs
	case Both:
		repl = make([]string, 0, len(blk.ours)+len(blk.theirs))
		repl = append(repl, blk.ours...)
		repl = append(repl, blk.theirs...)
	case Base:
		if !blk.hasBase {
			return ErrNoBase
		}
		repl = blk.base
	case Custom:
		for _, line := range custom {
			if strings.Contains(line, "\n") {
				return ErrBadLine
			}
		}
		repl = custom
	default:
		return ErrBadChoice
	}

	// Size check before mutating: only Custom can grow the document.
	newTotal := d.renderLinesLocked() - blockRenderLines(blk) + len(repl)
	if newTotal > d.maxLines {
		return ErrTooLarge
	}

	replCopy := append([]string(nil), repl...)
	d.replaceBlockWithText(idx, replCopy)
	return nil
}

// replaceBlockWithText replaces the block segment at idx with a body
// segment holding lines, merging with adjacent body segments.
func (d *Doc) replaceBlockWithText(idx int, lines []string) {
	if len(lines) == 0 {
		d.segs = append(d.segs[:idx], d.segs[idx+1:]...)
	} else {
		d.segs[idx] = segment{lines: lines}
	}
	d.mergeBodyAround(idx)
}

// mergeBodyAround merges any run of adjacent body segments that touches
// index idx.
func (d *Doc) mergeBodyAround(idx int) {
	// Find the start of the run of body segments containing idx.
	start := idx
	for start-1 >= 0 && !d.segs[start-1].isBlock {
		start--
	}
	if start >= len(d.segs) || d.segs[start].isBlock {
		return
	}
	end := start
	for end+1 < len(d.segs) && !d.segs[end+1].isBlock {
		end++
	}
	if end == start {
		return
	}
	var merged []string
	for _, seg := range d.segs[start : end+1] {
		merged = append(merged, seg.lines...)
	}
	d.segs = append(d.segs[:start], append([]segment{{lines: merged}}, d.segs[end+1:]...)...)
}

// commonPrefixLen returns the length of the longest common prefix of a
// and b.
func commonPrefixLen(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// commonSuffixLen returns the length of the longest common suffix of a
// and b.
func commonSuffixLen(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// equalLines reports whether a and b hold identical lines.
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

// appendBody appends lines to the last segment when it is a body
// segment, otherwise appends a new body segment.
func appendBody(segs []segment, lines []string) []segment {
	if len(lines) == 0 {
		return segs
	}
	if n := len(segs); n > 0 && !segs[n-1].isBlock {
		segs[n-1].lines = append(segs[n-1].lines, lines...)
		return segs
	}
	return append(segs, segment{lines: append([]string(nil), lines...)})
}

// normalizeLocked simplifies every block: the longest common prefix of
// ours/theirs moves out before the block, then the longest common
// suffix of the remaining lines moves out after it. Base drops lines
// only when its head matches the moved-out prefix exactly, and then its
// tail the moved-out suffix exactly. Blocks whose ours and theirs both
// become empty disappear.
func (d *Doc) normalizeLocked() {
	var out []segment
	for si := range d.segs {
		seg := d.segs[si]
		if !seg.isBlock {
			out = appendBody(out, seg.lines)
			continue
		}

		ours, theirs := seg.ours, seg.theirs

		// Prefix first.
		p := commonPrefixLen(ours, theirs)
		prefix := append([]string(nil), ours[:p]...)
		ours, theirs = ours[p:], theirs[p:]

		// Then the suffix of the remaining lines.
		s := commonSuffixLen(ours, theirs)
		suffix := append([]string(nil), ours[len(ours)-s:]...)
		ours, theirs = ours[:len(ours)-s], theirs[:len(theirs)-s]

		// Conditional base trimming.
		base := seg.base
		if seg.hasBase {
			if p > 0 && len(base) >= p && equalLines(base[:p], prefix) {
				base = base[p:]
			}
			if s > 0 && len(base) >= s && equalLines(base[len(base)-s:], suffix) {
				base = base[:len(base)-s]
			}
		}

		out = appendBody(out, prefix)
		if len(ours) > 0 || len(theirs) > 0 {
			nb := segment{
				isBlock: true,
				ours:    append([]string(nil), ours...),
				theirs:  append([]string(nil), theirs...),
				hasBase: seg.hasBase,
				lOpen:   seg.lOpen,
				lBase:   seg.lBase,
				lSep:    seg.lSep,
				lClose:  seg.lClose,
			}
			if seg.hasBase {
				nb.base = append([]string(nil), base...)
			}
			out = append(out, nb)
		}
		out = appendBody(out, suffix)
	}
	d.segs = out
}
