package pretty

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// LineInfo describes one rendered line whose width (after trimming trailing
// spaces) exceeds the line width. Overwide lines are reported, not errors.
type LineInfo struct {
	Line  int // 1-based line number
	Width int // trimmed width of the line
}

type mode int

const (
	modeBreak mode = iota
	modeFlat
)

// frame is one entry of the render command stack. The stack is a persistent
// singly linked list so the fits scan can walk the remaining commands
// without copying them (which would make deep nesting quadratic).
type frame struct {
	ind  int
	m    mode
	d    Doc
	next *frame
}

// fitsProbe counts fits-scan steps; used by tests to prove near-linear cost.
var fitsProbe atomic.Int64

type renderer struct {
	info   map[Doc]nodeInfo
	frags  map[string]Doc
	width  int
	col    int
	out    strings.Builder
	cur    []byte // current line, not yet trimmed
	curW   int    // width of cur, including trailing spaces
	raw    int    // total emitted width, for the safety cap
	total  int    // total trimmed width of finished lines
	lineNo int
	over   []LineInfo
}

// Render lays out root for the given line width and returns the rendered
// text plus the list of overwide lines. Rendering is read-only with respect
// to the session and the document tree, and is safe to run concurrently.
//
// Error priority: ErrInvalidArgument, ErrTooDeep, ErrUnregisteredRef,
// ErrOutputTooLarge.
func (s *Session) Render(root Doc, width int) (string, []LineInfo, error) {
	if width < 1 || width > maxLineWidth {
		return "", nil, fmt.Errorf("%w: line width %d outside [1, %d]", ErrInvalidArgument, width, maxLineWidth)
	}
	frags := s.snapshot()
	a := analyze(root, frags)
	switch {
	case a.invalid != nil:
		return "", nil, a.invalid
	case a.depthOf(root) > maxDepth:
		return "", nil, fmt.Errorf("%w: nesting depth exceeds %d", ErrTooDeep, maxDepth)
	case a.unregistered != "":
		return "", nil, fmt.Errorf("%w: %q", ErrUnregisteredRef, a.unregistered)
	}
	r := &renderer{info: a.info, frags: frags, width: width, lineNo: 1}
	return r.run(root)
}

func (r *renderer) run(root Doc) (string, []LineInfo, error) {
	frames := &frame{ind: 0, m: modeBreak, d: root}
	for frames != nil {
		f := frames
		frames = f.next
		var err error
		switch n := f.d.(type) {
		case nil:
			// Rejected by analysis; ignore defensively.
		case *textNode:
			err = r.emit(n.s, r.info[f.d].flatW)
		case *spaceNode:
			if f.m == modeFlat {
				err = r.emit(" ", 1)
			} else {
				err = r.newline(f.ind)
			}
		case *softNode:
			if f.m == modeBreak {
				err = r.newline(f.ind)
			}
		case *hardNode:
			err = r.newline(f.ind)
		case *indentNode:
			frames = &frame{ind: f.ind + n.n, m: f.m, d: n.body, next: frames}
		case *alignNode:
			frames = &frame{ind: r.col, m: f.m, d: n.body, next: frames}
		case *condNode:
			inf := r.info[f.d]
			if f.m == modeFlat {
				err = r.emit(n.flat, inf.flatW)
			} else {
				err = r.emit(n.broken, inf.altW)
			}
		case *seqNode:
			for i := len(n.parts) - 1; i >= 0; i-- {
				frames = &frame{ind: f.ind, m: f.m, d: n.parts[i], next: frames}
			}
		case *refNode:
			frames = &frame{ind: f.ind, m: f.m, d: r.frags[n.name], next: frames}
		case *groupNode:
			m := modeFlat
			inf := r.info[f.d]
			switch {
			case inf.hasHard:
				// A group containing a forced newline can never be flat.
				m = modeBreak
			case !r.fits(r.width-r.col, n.body, frames):
				m = modeBreak
			}
			frames = &frame{ind: f.ind, m: m, d: n.body, next: frames}
		}
		if err != nil {
			return "", nil, err
		}
	}
	if err := r.finishLine(); err != nil {
		return "", nil, err
	}
	return r.out.String(), r.over, nil
}

// fits reports whether rendering body flat, followed by the remaining
// commands up to the first breakable in break mode or forced newline (or
// the end of the document), keeps the accumulated width within remaining.
// It stops as soon as the accumulated width exceeds remaining.
func (r *renderer) fits(remaining int, body Doc, rest *frame) bool {
	total := 0
	type item struct {
		m mode
		d Doc
	}
	stack := []item{{m: modeFlat, d: body}}
	for {
		if len(stack) == 0 {
			if rest == nil {
				return total <= remaining
			}
			stack = append(stack, item{m: rest.m, d: rest.d})
			rest = rest.next
			continue
		}
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fitsProbe.Add(1)
		if it.d == nil {
			continue
		}
		inf := r.info[it.d]
		if !inf.hasStop {
			// Plain content (text and composites thereof): O(1).
			total += inf.flatW
			if total > remaining {
				return false
			}
			continue
		}
		switch n := it.d.(type) {
		case *spaceNode:
			if it.m == modeBreak {
				return total <= remaining
			}
			total++
		case *softNode:
			if it.m == modeBreak {
				return total <= remaining
			}
		case *hardNode:
			return total <= remaining
		case *indentNode:
			stack = append(stack, item{m: it.m, d: n.body})
		case *alignNode:
			stack = append(stack, item{m: it.m, d: n.body})
		case *condNode:
			if it.m == modeFlat {
				total += inf.flatW
			} else {
				total += inf.altW
			}
		case *seqNode:
			for i := len(n.parts) - 1; i >= 0; i-- {
				stack = append(stack, item{m: it.m, d: n.parts[i]})
			}
		case *refNode:
			stack = append(stack, item{m: it.m, d: r.frags[n.name]})
		case *groupNode:
			// Not yet decided: counts as flat. If it contains a forced
			// newline, only the part before that newline is accumulated
			// and the scan stops there.
			total += inf.headW
			if inf.hasHard {
				return total <= remaining
			}
		}
		if total > remaining {
			return false
		}
	}
}

func (r *renderer) emit(s string, w int) error {
	r.cur = append(r.cur, s...)
	r.col += w
	r.curW += w
	r.raw += w
	if r.raw > rawSafetyCap {
		return fmt.Errorf("%w: emitted output exceeds safety cap %d", ErrOutputTooLarge, rawSafetyCap)
	}
	return nil
}

func (r *renderer) newline(ind int) error {
	if err := r.finishLine(); err != nil {
		return err
	}
	r.out.WriteByte('\n')
	if ind > 0 {
		r.cur = append(r.cur, strings.Repeat(" ", ind)...)
	}
	r.col = ind
	r.curW = ind
	r.raw += ind
	if r.raw > rawSafetyCap {
		return fmt.Errorf("%w: emitted output exceeds safety cap %d", ErrOutputTooLarge, rawSafetyCap)
	}
	return nil
}

// finishLine trims trailing spaces off the current line, appends it to the
// output, and updates the overwide-line list and the total-width budget.
func (r *renderer) finishLine() error {
	trailing := 0
	for len(r.cur) > 0 && r.cur[len(r.cur)-1] == ' ' {
		r.cur = r.cur[:len(r.cur)-1]
		trailing++
	}
	w := r.curW - trailing
	r.out.Write(r.cur)
	r.total += w
	if r.total > maxTotalWidth {
		return fmt.Errorf("%w: total output width exceeds %d", ErrOutputTooLarge, maxTotalWidth)
	}
	if w > r.width {
		r.over = append(r.over, LineInfo{Line: r.lineNo, Width: w})
	}
	r.lineNo++
	r.cur = r.cur[:0]
	r.curW = 0
	return nil
}
