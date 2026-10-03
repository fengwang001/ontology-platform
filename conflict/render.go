package conflict

import "strings"

// minMarkerLen is the minimum marker length.
const minMarkerLen = 7

// leadingMarkerRun returns the length of the run of identical marker
// characters at the start of line (0 when the line does not start with
// a marker character).
func leadingMarkerRun(line string) int {
	if len(line) == 0 || !isMarkerChar(line[0]) {
		return 0
	}
	c := line[0]
	n := 0
	for n < len(line) && line[n] == c {
		n++
	}
	return n
}

// markerLenLocked computes L' = max(7, m+1) where m is the maximum
// leading marker run over all content lines (body lines and the ours,
// base and theirs lines of every block; marker lines are excluded).
func (d *Doc) markerLenLocked() int {
	m := 0
	scan := func(lines []string) {
		for _, line := range lines {
			if r := leadingMarkerRun(line); r > m {
				m = r
			}
		}
	}
	for _, seg := range d.segs {
		if seg.isBlock {
			scan(seg.ours)
			if seg.hasBase {
				scan(seg.base)
			}
			scan(seg.theirs)
		} else {
			scan(seg.lines)
		}
	}
	if m+1 > minMarkerLen {
		return m + 1
	}
	return minMarkerLen
}

// blockRenderLines returns the number of lines a block occupies when
// rendered, marker lines included.
func blockRenderLines(b *segment) int {
	n := len(b.ours) + len(b.theirs) + 3 // '<', '=' and '>' marker lines
	if b.hasBase {
		n += len(b.base) + 1 // base lines plus the '|' marker line
	}
	return n
}

// renderLinesLocked returns the total number of rendered lines.
func (d *Doc) renderLinesLocked() int {
	n := 0
	for i := range d.segs {
		seg := &d.segs[i]
		if seg.isBlock {
			n += blockRenderLines(seg)
		} else {
			n += len(seg.lines)
		}
	}
	return n
}

// writeMarker writes a marker line: L copies of c, plus a space and the
// label when the label is non-empty.
func writeMarker(sb *strings.Builder, c byte, L int, label string) {
	for i := 0; i < L; i++ {
		sb.WriteByte(c)
	}
	if label != "" {
		sb.WriteByte(' ')
		sb.WriteString(label)
	}
	sb.WriteByte('\n')
}

func writeLines(sb *strings.Builder, lines []string) {
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
}

func (d *Doc) renderLocked() string {
	L := d.markerLenLocked()
	var sb strings.Builder
	for i := range d.segs {
		seg := &d.segs[i]
		if !seg.isBlock {
			writeLines(&sb, seg.lines)
			continue
		}
		writeMarker(&sb, '<', L, seg.lOpen)
		writeLines(&sb, seg.ours)
		if seg.hasBase {
			writeMarker(&sb, '|', L, seg.lBase)
			writeLines(&sb, seg.base)
		}
		writeMarker(&sb, '=', L, seg.lSep)
		writeLines(&sb, seg.theirs)
		writeMarker(&sb, '>', L, seg.lClose)
	}
	return sb.String()
}

func (d *Doc) blocksLocked() int {
	n := 0
	for i := range d.segs {
		if d.segs[i].isBlock {
			n++
		}
	}
	return n
}
