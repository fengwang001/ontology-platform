package conflict

// This file holds a deliberately naive, independently structured reference
// implementation of the conflict document rules. It is used only by the
// differential tests: random documents and operation sequences are replayed
// against both implementations and must agree exactly.

import (
	"strings"
	"sync"
)

// nItem is one document item: either a single body line or one block.
// Unlike the real implementation, the naive one never merges body lines
// into shared segments; merging is not observable through the API.
type nItem struct {
	isBlock bool
	line    string
	blk     *nBlock
}

type nBlock struct {
	labels  [4]string // '<', '|', '=', '>'
	hasBase bool
	ours    []string
	base    []string
	theirs  []string
}

type nDoc struct {
	mu    sync.Mutex
	items []nItem
	max   int
}

func nIsMarkerChar(c byte) bool {
	return c == '<' || c == '|' || c == '=' || c == '>'
}

// nMarker reports whether line is a marker line for length L and, if so,
// returns its character and label (trailing spaces dropped).
func nMarker(line string, L int) (byte, string, bool) {
	if len(line) < L || !nIsMarkerChar(line[0]) {
		return 0, "", false
	}
	for i := 0; i < L; i++ {
		if line[i] != line[0] {
			return 0, "", false
		}
	}
	if len(line) > L {
		if line[L] != ' ' {
			return 0, "", false
		}
		return line[0], strings.TrimRight(line[L+1:], " "), true
	}
	return line[0], "", true
}

func nClampMax(n int) int {
	if n < 1 {
		return 1
	}
	if n > 1000000 {
		return 1000000
	}
	return n
}

func nParse(text string, L, maxLines int) (*nDoc, error) {
	if L < 7 {
		return nil, &Error{Kind: ErrBadL}
	}
	d := &nDoc{max: nClampMax(maxLines)}
	if text == "" {
		return d, nil
	}
	lines := strings.Split(text, "\n")
	missingNewline := lines[len(lines)-1] != ""
	if !missingNewline {
		lines = lines[:len(lines)-1]
	}

	var cur *nBlock
	phase := 0 // 0 ours, 1 base, 2 theirs
	start := 0
	for i, ln := range lines {
		lineNo := i + 1
		c, label, isM := nMarker(ln, L)
		if cur == nil {
			if !isM {
				d.items = append(d.items, nItem{line: ln})
				continue
			}
			if c != '<' {
				return nil, &Error{Kind: ErrStray, Line: lineNo}
			}
			cur = &nBlock{}
			cur.labels[0] = label
			phase = 0
			start = lineNo
			continue
		}
		if isM {
			switch c {
			case '<':
				return nil, &Error{Kind: ErrNested, Line: lineNo}
			case '|':
				if phase != 0 {
					return nil, &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.hasBase = true
				cur.labels[1] = label
				phase = 1
			case '=':
				if phase == 2 {
					return nil, &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.labels[2] = label
				phase = 2
			case '>':
				if phase != 2 {
					return nil, &Error{Kind: ErrOrder, Line: lineNo}
				}
				cur.labels[3] = label
				d.items = append(d.items, nItem{isBlock: true, blk: cur})
				cur = nil
			}
			continue
		}
		switch phase {
		case 0:
			cur.ours = append(cur.ours, ln)
		case 1:
			cur.base = append(cur.base, ln)
		case 2:
			cur.theirs = append(cur.theirs, ln)
		}
	}
	if cur != nil {
		return nil, &Error{Kind: ErrUnterminated, Line: start}
	}
	if missingNewline {
		return nil, &Error{Kind: ErrNoNewline, Line: len(lines)}
	}
	if nRenderedLines(d.items) > d.max {
		return nil, &Error{Kind: ErrTooLarge}
	}
	return d, nil
}

func nRenderedLines(items []nItem) int {
	n := 0
	for _, it := range items {
		if !it.isBlock {
			n++
			continue
		}
		b := it.blk
		n += 3 + len(b.ours) + len(b.theirs)
		if b.hasBase {
			n += 1 + len(b.base)
		}
	}
	return n
}

func (d *nDoc) blocks() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, it := range d.items {
		if it.isBlock {
			n++
		}
	}
	return n
}

func (d *nDoc) resolve(i int, choice Choice, custom []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := -1
	seen := 0
	for j, it := range d.items {
		if !it.isBlock {
			continue
		}
		if seen == i {
			idx = j
			break
		}
		seen++
	}
	if i < 0 || idx < 0 {
		return &Error{Kind: ErrNoSuchBlock}
	}
	b := d.items[idx].blk
	var repl []string
	switch choice {
	case Ours:
		repl = b.ours
	case Theirs:
		repl = b.theirs
	case Both:
		repl = append(append([]string{}, b.ours...), b.theirs...)
	case Base:
		if !b.hasBase {
			return &Error{Kind: ErrNoBase}
		}
		repl = b.base
	case Custom:
		for _, ln := range custom {
			if strings.Contains(ln, "\n") {
				return &Error{Kind: ErrBadLine}
			}
		}
		repl = custom
	default:
		return &Error{Kind: ErrBadLine}
	}
	var out []nItem
	out = append(out, d.items[:idx]...)
	for _, ln := range repl {
		out = append(out, nItem{line: ln})
	}
	out = append(out, d.items[idx+1:]...)
	if nRenderedLines(out) > d.max {
		return &Error{Kind: ErrTooLarge}
	}
	d.items = out
	return nil
}

func (d *nDoc) normalize() {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []nItem
	emit := func(lines []string) {
		for _, ln := range lines {
			out = append(out, nItem{line: ln})
		}
	}
	for _, it := range d.items {
		if !it.isBlock {
			out = append(out, it)
			continue
		}
		b := it.blk
		// Longest common prefix of ours and theirs.
		p := 0
		for p < len(b.ours) && p < len(b.theirs) && b.ours[p] == b.theirs[p] {
			p++
		}
		prefix := append([]string{}, b.ours[:p]...)
		ours := append([]string{}, b.ours[p:]...)
		theirs := append([]string{}, b.theirs[p:]...)
		// Longest common suffix of the remainders.
		s := 0
		for s < len(ours) && s < len(theirs) && ours[len(ours)-1-s] == theirs[len(theirs)-1-s] {
			s++
		}
		suffix := append([]string{}, ours[len(ours)-s:]...)
		ours = ours[:len(ours)-s]
		theirs = theirs[:len(theirs)-s]
		// Conditional base trimming, prefix first then suffix.
		base := append([]string{}, b.base...)
		if b.hasBase {
			match := p > 0 && len(base) >= p
			if match {
				for k := 0; k < p; k++ {
					if base[k] != prefix[k] {
						match = false
					}
				}
			}
			if match {
				base = base[p:]
			}
			match = s > 0 && len(base) >= s
			if match {
				for k := 0; k < s; k++ {
					if base[len(base)-s+k] != suffix[k] {
						match = false
					}
				}
			}
			if match {
				base = base[:len(base)-s]
			}
		}
		emit(prefix)
		if len(ours) == 0 && len(theirs) == 0 {
			emit(suffix)
			continue
		}
		out = append(out, nItem{isBlock: true, blk: &nBlock{
			labels:  b.labels,
			hasBase: b.hasBase,
			ours:    ours,
			base:    base,
			theirs:  theirs,
		}})
		emit(suffix)
	}
	d.items = out
}

func nLeadingRun(s string) int {
	if len(s) == 0 || !nIsMarkerChar(s[0]) {
		return 0
	}
	n := 1
	for n < len(s) && s[n] == s[0] {
		n++
	}
	return n
}

func (d *nDoc) render() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := 0
	for _, it := range d.items {
		var lines []string
		if !it.isBlock {
			lines = []string{it.line}
		} else {
			lines = append(append(append([]string{}, it.blk.ours...), it.blk.base...), it.blk.theirs...)
		}
		for _, ln := range lines {
			if r := nLeadingRun(ln); r > m {
				m = r
			}
		}
	}
	L := 7
	if m+1 > L {
		L = m + 1
	}
	var sb strings.Builder
	mark := func(c byte, label string) {
		for k := 0; k < L; k++ {
			sb.WriteByte(c)
		}
		if label != "" {
			sb.WriteByte(' ')
			sb.WriteString(label)
		}
		sb.WriteByte('\n')
	}
	for _, it := range d.items {
		if !it.isBlock {
			sb.WriteString(it.line)
			sb.WriteByte('\n')
			continue
		}
		b := it.blk
		mark('<', b.labels[0])
		for _, ln := range b.ours {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
		if b.hasBase {
			mark('|', b.labels[1])
			for _, ln := range b.base {
				sb.WriteString(ln)
				sb.WriteByte('\n')
			}
		}
		mark('=', b.labels[2])
		for _, ln := range b.theirs {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
		mark('>', b.labels[3])
	}
	return sb.String()
}
