package conflict

import "strings"

// parse state
const (
	stOutside = iota // between blocks
	stOurs           // after '<', before '|' or '='
	stBase           // after '|', before '='
	stTheirs         // after '=', before '>'
)

// isMarkerChar reports whether c is one of the four marker characters.
func isMarkerChar(c byte) bool {
	return c == '<' || c == '|' || c == '=' || c == '>'
}

// splitLines splits text into lines without their trailing "\n".
// ok is false when a non-empty text does not end with "\n".
func splitLines(text string) (lines []string, ok bool) {
	if text == "" {
		return nil, true
	}
	ok = strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	if ok {
		parts = parts[:len(parts)-1]
	}
	return parts, ok
}

// markerLine reports whether line is a marker line for marker length L:
// exactly L identical marker characters followed by end of line or a
// space. It returns the marker character and the label (the text after
// the first space, with trailing spaces dropped).
func markerLine(line string, L int) (c byte, label string, ok bool) {
	if len(line) == 0 || !isMarkerChar(line[0]) {
		return 0, "", false
	}
	c = line[0]
	n := 0
	for n < len(line) && line[n] == c {
		n++
	}
	if n != L {
		return 0, "", false
	}
	rest := line[L:]
	if rest == "" {
		return c, "", true
	}
	if rest[0] != ' ' {
		return 0, "", false
	}
	return c, strings.TrimRight(rest[1:], " "), true
}

// parseError is a candidate parse error at a 1-based line.
type parseError struct {
	line int
	err  error
}

// less orders candidates: smallest line wins; on a tie ErrNoNewline
// sorts last.
func (a parseError) less(b parseError) bool {
	if a.line != b.line {
		return a.line < b.line
	}
	return b.err == ErrNoNewline && a.err != ErrNoNewline
}

// parse runs the syntax analysis and returns the document segments or
// the winning parse error.
func parse(text string, L int) ([]segment, error) {
	lines, nlOK := splitLines(text)

	var segs []segment
	var body []string // accumulated body lines of the current segment
	var blk segment   // block under construction
	state := stOutside
	blkStart := 0 // 1-based line of the current block's '<' marker

	var errs []parseError
	fail := func(line int, err error) { errs = append(errs, parseError{line, err}) }

	flushBody := func() {
		if len(body) > 0 {
			segs = append(segs, segment{lines: body})
			body = nil
		}
	}

	for i, line := range lines {
		ln := i + 1
		c, label, isMarker := markerLine(line, L)
		if !isMarker {
			switch state {
			case stOutside:
				body = append(body, line)
			case stOurs:
				blk.ours = append(blk.ours, line)
			case stBase:
				blk.base = append(blk.base, line)
			case stTheirs:
				blk.theirs = append(blk.theirs, line)
			}
			continue
		}
		switch state {
		case stOutside:
			if c == '<' {
				flushBody()
				blk = segment{isBlock: true, lOpen: label}
				blkStart = ln
				state = stOurs
			} else {
				fail(ln, ErrStray)
				body = append(body, line)
			}
		case stOurs:
			switch c {
			case '<':
				fail(ln, ErrNested)
				blk.ours = append(blk.ours, line)
			case '|':
				blk.hasBase = true
				blk.lBase = label
				state = stBase
			case '=':
				blk.lSep = label
				state = stTheirs
			case '>':
				fail(ln, ErrOrder)
				blk.ours = append(blk.ours, line)
			}
		case stBase:
			switch c {
			case '<':
				fail(ln, ErrNested)
				blk.base = append(blk.base, line)
			case '|':
				fail(ln, ErrOrder)
				blk.base = append(blk.base, line)
			case '=':
				blk.lSep = label
				state = stTheirs
			case '>':
				fail(ln, ErrOrder)
				blk.base = append(blk.base, line)
			}
		case stTheirs:
			switch c {
			case '<':
				fail(ln, ErrNested)
				blk.theirs = append(blk.theirs, line)
			case '|', '=':
				fail(ln, ErrOrder)
				blk.theirs = append(blk.theirs, line)
			case '>':
				blk.lClose = label
				segs = append(segs, blk)
				blk = segment{}
				state = stOutside
			}
		}
	}

	flushBody()
	if state != stOutside {
		fail(blkStart, ErrUnterminated)
	}
	if !nlOK {
		fail(len(lines), ErrNoNewline)
	}
	if len(errs) > 0 {
		best := errs[0]
		for _, e := range errs[1:] {
			if e.less(best) {
				best = e
			}
		}
		return nil, &LineError{Err: best.err, Line: best.line}
	}
	return segs, nil
}
