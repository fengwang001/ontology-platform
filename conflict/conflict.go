// Package conflict parses, resolves, normalizes and renders diff3-style
// conflict-marker documents.
package conflict

import (
	"fmt"
	"strings"
	"sync"
)

// Kind identifies the category of an Error.
type Kind int

const (
	// ErrBadL indicates Parse was called with L < 7.
	ErrBadL Kind = iota + 1
	// ErrStray indicates a '|', '=' or '>' marker line outside any block.
	ErrStray
	// ErrNested indicates a '<' marker line inside a block.
	ErrNested
	// ErrOrder indicates a marker line violating the block-internal order.
	ErrOrder
	// ErrUnterminated indicates a block still open at end of file.
	ErrUnterminated
	// ErrNoNewline indicates a non-empty text whose last line lacks "\n".
	ErrNoNewline
	// ErrTooLarge indicates the rendered line count exceeds MaxLines.
	ErrTooLarge
	// ErrNoSuchBlock indicates Resolve was given an out-of-range index.
	ErrNoSuchBlock
	// ErrNoBase indicates Resolve with Base on a block without a '|' line.
	ErrNoBase
	// ErrBadLine indicates a Custom replacement line containing "\n".
	ErrBadLine
)

// Error describes a parse or resolve failure. Line is 1-based and only
// meaningful for parse errors; it is 0 otherwise.
type Error struct {
	Kind Kind
	Line int
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("conflict: %s at line %d", e.Kind, e.Line)
	}
	return fmt.Sprintf("conflict: %s", e.Kind)
}

func (k Kind) String() string {
	switch k {
	case ErrBadL:
		return "ErrBadL"
	case ErrStray:
		return "ErrStray"
	case ErrNested:
		return "ErrNested"
	case ErrOrder:
		return "ErrOrder"
	case ErrUnterminated:
		return "ErrUnterminated"
	case ErrNoNewline:
		return "ErrNoNewline"
	case ErrTooLarge:
		return "ErrTooLarge"
	case ErrNoSuchBlock:
		return "ErrNoSuchBlock"
	case ErrNoBase:
		return "ErrNoBase"
	case ErrBadLine:
		return "ErrBadLine"
	}
	return "ErrUnknown"
}

// Choice selects how a block is resolved.
type Choice int

const (
	Ours Choice = iota
	Theirs
	Both
	Base
	Custom
)

// Doc is a parsed conflict document. All methods are safe for concurrent
// use; concurrent calls behave as if executed in some serial order.
type Doc struct {
	mu       sync.Mutex
	segs     []segment
	maxLines int
}

const (
	minL        = 7
	maxMaxLines = 1_000_000
)

func clampMaxLines(n int) int {
	if n < 1 {
		return 1
	}
	if n > maxMaxLines {
		return maxMaxLines
	}
	return n
}

func isMarkerChar(c byte) bool {
	return c == '<' || c == '|' || c == '=' || c == '>'
}

// leadingRun returns the number of consecutive identical marker characters
// at the start of s (0 if s does not start with a marker character).
func leadingRun(s string) int {
	if len(s) == 0 || !isMarkerChar(s[0]) {
		return 0
	}
	n := 1
	for n < len(s) && s[n] == s[0] {
		n++
	}
	return n
}

// marker classifies line as a marker line for marker length L. A marker
// line starts with exactly L identical marker characters followed by end of
// line or a single space; the label is everything after that first space
// with trailing spaces dropped.
func marker(line string, L int) (c byte, label string, ok bool) {
	if len(line) < L || !isMarkerChar(line[0]) {
		return 0, "", false
	}
	c = line[0]
	for i := 1; i < L; i++ {
		if line[i] != c {
			return 0, "", false
		}
	}
	if len(line) > L && line[L] != ' ' {
		return 0, "", false
	}
	if len(line) > L {
		label = strings.TrimRight(line[L+1:], " ")
	}
	return c, label, true
}

// Parse parses text with marker length L (>= 7). maxLines bounds the
// number of rendered lines (1..10^6, clamped).
func Parse(text string, L int, maxLines int) (*Doc, error) {
	if L < minL {
		return nil, &Error{Kind: ErrBadL}
	}
	d := &Doc{maxLines: clampMaxLines(maxLines)}
	if text == "" {
		return d, nil
	}
	parts := strings.Split(text, "\n")
	noNewline := parts[len(parts)-1] != ""
	if !noNewline {
		parts = parts[:len(parts)-1]
	}
	if err := d.build(parts, L); err != nil {
		return nil, err
	}
	if noNewline {
		return nil, &Error{Kind: ErrNoNewline, Line: len(parts)}
	}
	if renderedLines(d.segs) > d.maxLines {
		return nil, &Error{Kind: ErrTooLarge}
	}
	return d, nil
}

// build runs the structural parse over lines (each without the trailing
// "\n") and fills d.segs. It returns the first structural error, which is
// also the one with the smallest line number.
func (d *Doc) build(lines []string, L int) error {
	var pending []string // accumulated body lines
	flush := func() {
		if len(pending) > 0 {
			d.segs = append(d.segs, segment{lines: pending})
			pending = nil
		}
	}

	const (
		phaseOurs = iota
		phaseBase
		phaseTheirs
	)
	var cur *block
	phase := phaseOurs
	startLine := 0

	for idx, line := range lines {
		lineNo := idx + 1
		c, label, isMarker := marker(line, L)
		if cur == nil {
			if !isMarker {
				pending = append(pending, line)
				continue
			}
			switch c {
			case '<':
				flush()
				cur = &block{startLabel: label}
				phase = phaseOurs
				startLine = lineNo
			default:
				return &Error{Kind: ErrStray, Line: lineNo}
			}
			continue
		}
		// Inside a block.
		if isMarker {
			switch c {
			case '<':
				return &Error{Kind: ErrNested, Line: lineNo}
			case '|':
				if phase != phaseOurs {
					return &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.hasBase = true
				cur.baseLabel = label
				phase = phaseBase
			case '=':
				if phase == phaseTheirs {
					return &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.sepLabel = label
				phase = phaseTheirs
			case '>':
				if phase != phaseTheirs {
					return &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.endLabel = label
				d.segs = append(d.segs, segment{blk: cur})
				cur = nil
			}
			continue
		}
		switch phase {
		case phaseOurs:
			cur.ours = append(cur.ours, line)
		case phaseBase:
			cur.base = append(cur.base, line)
		case phaseTheirs:
			cur.theirs = append(cur.theirs, line)
		}
	}
	if cur != nil {
		return &Error{Kind: ErrUnterminated, Line: startLine}
	}
	flush()
	return nil
}

// renderedLines counts the lines Render would emit for segs.
func renderedLines(segs []segment) int {
	n := 0
	for _, s := range segs {
		if s.blk == nil {
			n += len(s.lines)
			continue
		}
		b := s.blk
		n += 3 + len(b.ours) + len(b.theirs)
		if b.hasBase {
			n += 1 + len(b.base)
		}
	}
	return n
}

// markerLen computes L' = max(7, m+1) where m is the maximum leading run
// of identical marker characters over all content lines.
func markerLen(segs []segment) int {
	m := 0
	scan := func(lines []string) {
		for _, ln := range lines {
			if r := leadingRun(ln); r > m {
				m = r
			}
		}
	}
	for _, s := range segs {
		if s.blk == nil {
			scan(s.lines)
			continue
		}
		scan(s.blk.ours)
		scan(s.blk.base)
		scan(s.blk.theirs)
	}
	if m+1 > minL {
		return m + 1
	}
	return minL
}

// Blocks returns the number of currently unresolved blocks.
func (d *Doc) Blocks() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, s := range d.segs {
		if s.blk != nil {
			n++
		}
	}
	return n
}

// Resolve resolves the i-th (0-based) unresolved block.
func (d *Doc) Resolve(i int, choice Choice, custom []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	segIdx := -1
	seen := 0
	for j, s := range d.segs {
		if s.blk == nil {
			continue
		}
		if seen == i {
			segIdx = j
			break
		}
		seen++
	}
	if i < 0 || segIdx < 0 {
		return &Error{Kind: ErrNoSuchBlock}
	}
	b := d.segs[segIdx].blk

	var repl []string
	switch choice {
	case Ours:
		repl = append(repl, b.ours...)
	case Theirs:
		repl = append(repl, b.theirs...)
	case Both:
		repl = append(repl, b.ours...)
		repl = append(repl, b.theirs...)
	case Base:
		if !b.hasBase {
			return &Error{Kind: ErrNoBase}
		}
		repl = append(repl, b.base...)
	case Custom:
		for _, ln := range custom {
			if strings.Contains(ln, "\n") {
				return &Error{Kind: ErrBadLine}
			}
		}
		repl = append(repl, custom...)
	default:
		return &Error{Kind: ErrBadLine}
	}

	// Build the replacement segment list, merging the replacement lines
	// with adjacent body segments.
	newSegs := make([]segment, 0, len(d.segs))
	merged := append([]string(nil), repl...)
	if segIdx+1 < len(d.segs) && d.segs[segIdx+1].blk == nil {
		merged = append(merged, d.segs[segIdx+1].lines...)
	}
	newSegs = append(newSegs, d.segs[:segIdx]...)
	if len(merged) > 0 {
		if len(newSegs) > 0 && newSegs[len(newSegs)-1].blk == nil {
			newSegs[len(newSegs)-1].lines = append(newSegs[len(newSegs)-1].lines, merged...)
		} else {
			newSegs = append(newSegs, segment{lines: merged})
		}
	}
	if segIdx+1 < len(d.segs) {
		rest := d.segs[segIdx+1:]
		if rest[0].blk == nil {
			rest = rest[1:]
		}
		newSegs = append(newSegs, rest...)
	}

	if renderedLines(newSegs) > d.maxLines {
		return &Error{Kind: ErrTooLarge}
	}
	d.segs = newSegs
	return nil
}

// Normalize moves common prefixes/suffixes out of every block.
func (d *Doc) Normalize() {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]segment, 0, len(d.segs))
	appendText := func(lines []string) {
		if len(lines) == 0 {
			return
		}
		if len(out) > 0 && out[len(out)-1].blk == nil {
			out[len(out)-1].lines = append(out[len(out)-1].lines, lines...)
			return
		}
		out = append(out, segment{lines: append([]string(nil), lines...)})
	}

	for _, s := range d.segs {
		if s.blk == nil {
			appendText(s.lines)
			continue
		}
		b := s.blk
		p := commonPrefixLen(b.ours, b.theirs)
		prefix := b.ours[:p]
		ours := b.ours[p:]
		theirs := b.theirs[p:]
		s := commonSuffixLen(ours, theirs)
		suffix := ours[len(ours)-s:]
		ours = ours[:len(ours)-s]
		theirs = theirs[:len(theirs)-s]

		base := b.base
		if b.hasBase {
			if p > 0 && len(base) >= p && equalLines(base[:p], prefix) {
				base = base[p:]
			}
			if s > 0 && len(base) >= s && equalLines(base[len(base)-s:], suffix) {
				base = base[:len(base)-s]
			}
		}

		appendText(prefix)
		if len(ours) == 0 && len(theirs) == 0 {
			appendText(suffix)
			continue
		}
		out = append(out, segment{blk: &block{
			startLabel: b.startLabel,
			baseLabel:  b.baseLabel,
			sepLabel:   b.sepLabel,
			endLabel:   b.endLabel,
			hasBase:    b.hasBase,
			ours:       append([]string(nil), ours...),
			base:       append([]string(nil), base...),
			theirs:     append([]string(nil), theirs...),
		}})
		appendText(suffix)
	}
	d.segs = out
}

func commonPrefixLen(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffixLen(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
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

// Render renders the document back to text.
func (d *Doc) Render() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	L := markerLen(d.segs)
	marks := map[byte]string{
		'<': strings.Repeat("<", L),
		'|': strings.Repeat("|", L),
		'=': strings.Repeat("=", L),
		'>': strings.Repeat(">", L),
	}
	var sb strings.Builder
	writeMarker := func(c byte, label string) {
		sb.WriteString(marks[c])
		if label != "" {
			sb.WriteByte(' ')
			sb.WriteString(label)
		}
		sb.WriteByte('\n')
	}
	writeLines := func(lines []string) {
		for _, ln := range lines {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
	}
	for _, s := range d.segs {
		if s.blk == nil {
			writeLines(s.lines)
			continue
		}
		b := s.blk
		writeMarker('<', b.startLabel)
		writeLines(b.ours)
		if b.hasBase {
			writeMarker('|', b.baseLabel)
			writeLines(b.base)
		}
		writeMarker('=', b.sepLabel)
		writeLines(b.theirs)
		writeMarker('>', b.endLabel)
	}
	return sb.String()
}
