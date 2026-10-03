package conflict

// This file holds the naive reference implementation used by the
// randomized differential test. It follows the specification rules
// step by step, deliberately written independently from the optimized
// implementation in the package sources.

import (
	"errors"
	"strings"
)

type nSeg struct {
	block   bool
	lines   []string // body lines
	ours    []string
	base    []string
	hasBase bool
	theirs  []string
	lOpen   string
	lBase   string
	lSep    string
	lClose  string
}

type nDoc struct {
	segs     []nSeg
	maxLines int
}

func nIsMarker(c byte) bool {
	return c == '<' || c == '|' || c == '=' || c == '>'
}

// nMarker classifies a line against marker length L, step by step.
func nMarker(line string, L int) (byte, string, bool) {
	if line == "" {
		return 0, "", false
	}
	c := line[0]
	if !nIsMarker(c) {
		return 0, "", false
	}
	n := 0
	for n < len(line) && line[n] == c {
		n++
	}
	if n < L || n > L {
		return 0, "", false // fewer or more than L: body
	}
	rest := line[L:]
	if rest == "" {
		return c, "", true
	}
	if rest[0] != ' ' {
		return 0, "", false
	}
	label := rest[1:]
	for strings.HasSuffix(label, " ") {
		label = label[:len(label)-1]
	}
	return c, label, true
}

type nErr struct {
	line int
	err  error
}

func naiveParse(text string, L, maxLines int) (*nDoc, error) {
	if L < 7 {
		return nil, ErrBadL
	}
	// Split into lines.
	var lines []string
	nlOK := true
	if text != "" {
		nlOK = strings.HasSuffix(text, "\n")
		lines = strings.Split(text, "\n")
		if nlOK {
			lines = lines[:len(lines)-1]
		}
	}

	var errs []nErr
	var segs []nSeg
	var body []string
	var cur nSeg
	state := 0 // 0 outside, 1 ours, 2 base, 3 theirs
	blkStart := 0

	flush := func() {
		if len(body) > 0 {
			segs = append(segs, nSeg{lines: body})
			body = nil
		}
	}

	for i, line := range lines {
		ln := i + 1
		c, label, m := nMarker(line, L)
		if !m {
			switch state {
			case 0:
				body = append(body, line)
			case 1:
				cur.ours = append(cur.ours, line)
			case 2:
				cur.base = append(cur.base, line)
			case 3:
				cur.theirs = append(cur.theirs, line)
			}
			continue
		}
		switch state {
		case 0:
			if c == '<' {
				flush()
				cur = nSeg{block: true, lOpen: label}
				blkStart = ln
				state = 1
			} else {
				errs = append(errs, nErr{ln, ErrStray})
				body = append(body, line)
			}
		case 1:
			switch c {
			case '<':
				errs = append(errs, nErr{ln, ErrNested})
				cur.ours = append(cur.ours, line)
			case '|':
				cur.hasBase = true
				cur.lBase = label
				state = 2
			case '=':
				cur.lSep = label
				state = 3
			case '>':
				errs = append(errs, nErr{ln, ErrOrder})
				cur.ours = append(cur.ours, line)
			}
		case 2:
			switch c {
			case '<':
				errs = append(errs, nErr{ln, ErrNested})
				cur.base = append(cur.base, line)
			case '|':
				errs = append(errs, nErr{ln, ErrOrder})
				cur.base = append(cur.base, line)
			case '=':
				cur.lSep = label
				state = 3
			case '>':
				errs = append(errs, nErr{ln, ErrOrder})
				cur.base = append(cur.base, line)
			}
		case 3:
			switch c {
			case '<':
				errs = append(errs, nErr{ln, ErrNested})
				cur.theirs = append(cur.theirs, line)
			case '|', '=':
				errs = append(errs, nErr{ln, ErrOrder})
				cur.theirs = append(cur.theirs, line)
			case '>':
				cur.lClose = label
				segs = append(segs, cur)
				cur = nSeg{}
				state = 0
			}
		}
	}
	flush()
	if state != 0 {
		errs = append(errs, nErr{blkStart, ErrUnterminated})
	}
	if !nlOK {
		errs = append(errs, nErr{len(lines), ErrNoNewline})
	}
	if len(errs) > 0 {
		best := errs[0]
		for _, e := range errs[1:] {
			if e.line < best.line ||
				(e.line == best.line && best.err == ErrNoNewline && e.err != ErrNoNewline) {
				best = e
			}
		}
		return nil, &LineError{Err: best.err, Line: best.line}
	}

	d := &nDoc{segs: segs, maxLines: maxLines}
	if d.nRenderLines() > maxLines {
		return nil, ErrTooLarge
	}
	return d, nil
}

func nLeadingRun(line string) int {
	if line == "" || !nIsMarker(line[0]) {
		return 0
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	return n
}

func (d *nDoc) nMarkerLen() int {
	m := 0
	bump := func(lines []string) {
		for _, l := range lines {
			if r := nLeadingRun(l); r > m {
				m = r
			}
		}
	}
	for _, s := range d.segs {
		if s.block {
			bump(s.ours)
			if s.hasBase {
				bump(s.base)
			}
			bump(s.theirs)
		} else {
			bump(s.lines)
		}
	}
	if m+1 > 7 {
		return m + 1
	}
	return 7
}

func (d *nDoc) nRenderLines() int {
	n := 0
	for _, s := range d.segs {
		if s.block {
			n += len(s.ours) + len(s.theirs) + 3
			if s.hasBase {
				n += len(s.base) + 1
			}
		} else {
			n += len(s.lines)
		}
	}
	return n
}

func (d *nDoc) nRender() string {
	L := d.nMarkerLen()
	var sb strings.Builder
	marker := func(c byte, label string) {
		sb.WriteString(strings.Repeat(string(c), L))
		if label != "" {
			sb.WriteByte(' ')
			sb.WriteString(label)
		}
		sb.WriteByte('\n')
	}
	put := func(lines []string) {
		for _, l := range lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	for _, s := range d.segs {
		if !s.block {
			put(s.lines)
			continue
		}
		marker('<', s.lOpen)
		put(s.ours)
		if s.hasBase {
			marker('|', s.lBase)
			put(s.base)
		}
		marker('=', s.lSep)
		put(s.theirs)
		marker('>', s.lClose)
	}
	return sb.String()
}

func (d *nDoc) nBlocks() int {
	n := 0
	for _, s := range d.segs {
		if s.block {
			n++
		}
	}
	return n
}

func (d *nDoc) nResolve(i int, choice Choice, custom []string) error {
	if i < 0 || i >= d.nBlocks() {
		return ErrNoSuchBlock
	}
	// Find the i-th block.
	idx := 0
	seen := 0
	for j, s := range d.segs {
		if s.block {
			if seen == i {
				idx = j
				break
			}
			seen++
		}
	}
	blk := d.segs[idx]
	var repl []string
	switch choice {
	case Ours:
		repl = append(repl, blk.ours...)
	case Theirs:
		repl = append(repl, blk.theirs...)
	case Both:
		repl = append(repl, blk.ours...)
		repl = append(repl, blk.theirs...)
	case Base:
		if !blk.hasBase {
			return ErrNoBase
		}
		repl = append(repl, blk.base...)
	case Custom:
		for _, l := range custom {
			if strings.Contains(l, "\n") {
				return ErrBadLine
			}
		}
		repl = append(repl, custom...)
	default:
		return ErrBadChoice
	}
	// Simulate the size on a scratch copy.
	scratch := &nDoc{maxLines: d.maxLines}
	scratch.segs = append(scratch.segs, d.segs[:idx]...)
	scratch.segs = append(scratch.segs, nSeg{lines: repl})
	scratch.segs = append(scratch.segs, d.segs[idx+1:]...)
	if scratch.nRenderLines() > d.maxLines {
		return ErrTooLarge
	}
	// Splice in the replacement and merge adjacent bodies.
	var out []nSeg
	out = append(out, d.segs[:idx]...)
	out = append(out, nSeg{lines: repl})
	out = append(out, d.segs[idx+1:]...)
	d.segs = nMerge(out)
	return nil
}

// nMerge merges adjacent body segments and drops empty ones.
func nMerge(segs []nSeg) []nSeg {
	var out []nSeg
	for _, s := range segs {
		if s.block {
			out = append(out, s)
			continue
		}
		if len(s.lines) == 0 {
			continue
		}
		if n := len(out); n > 0 && !out[n-1].block {
			out[n-1].lines = append(out[n-1].lines, s.lines...)
			continue
		}
		out = append(out, s)
	}
	return out
}

func nCommonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func nCommonSuffix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func nEqual(a, b []string) bool {
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

func (d *nDoc) nNormalize() {
	var out []nSeg
	emitBody := func(lines []string) {
		if len(lines) > 0 {
			out = append(out, nSeg{lines: append([]string(nil), lines...)})
		}
	}
	for _, s := range d.segs {
		if !s.block {
			emitBody(s.lines)
			continue
		}
		ours := append([]string(nil), s.ours...)
		theirs := append([]string(nil), s.theirs...)
		base := append([]string(nil), s.base...)

		// Step 1: longest common prefix moves out before the block.
		p := nCommonPrefix(ours, theirs)
		prefix := append([]string(nil), ours[:p]...)
		ours = ours[p:]
		theirs = theirs[p:]

		// Step 2: longest common suffix of the remainder moves out.
		suf := nCommonSuffix(ours, theirs)
		suffix := append([]string(nil), ours[len(ours)-suf:]...)
		ours = ours[:len(ours)-suf]
		theirs = theirs[:len(theirs)-suf]

		// Step 3: conditional base trimming.
		if s.hasBase {
			if p > 0 && len(base) >= p && nEqual(base[:p], prefix) {
				base = base[p:]
			}
			if suf > 0 && len(base) >= suf && nEqual(base[len(base)-suf:], suffix) {
				base = base[:len(base)-suf]
			}
		}

		emitBody(prefix)
		if len(ours) > 0 || len(theirs) > 0 {
			nb := s
			nb.ours, nb.theirs, nb.base = ours, theirs, base
			out = append(out, nb)
		}
		emitBody(suffix)
	}
	d.segs = nMerge(out)
}

// nErrString renders an error (or nil) for comparison.
func nErrString(err error) string {
	if err == nil {
		return "<nil>"
	}
	var le *LineError
	if errors.As(err, &le) {
		return le.Err.Error() + "@" + itoa(le.Line)
	}
	return err.Error()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
