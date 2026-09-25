package patch

import (
	"errors"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Distinguishable failure categories.
var (
	ErrFormat       = errors.New("patch: malformed patch")
	ErrContext      = errors.New("patch: context mismatch")
	ErrOutOfFuzz    = errors.New("patch: no match within offset fuzz")
	ErrTooDifferent = errors.New("patch: inputs differ by more than allowed distance")
)

// ApplyError identifies the failing hunk and the category.
type ApplyError struct {
	Hunk     int // 0-based hunk index
	Category error
	Reason   string
}

func (e *ApplyError) Error() string {
	return "patch: hunk #" + itoa(e.Hunk) + ": " + e.Category.Error() + ": " + e.Reason
}
func (e *ApplyError) Unwrap() error { return e.Category }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// Options controls application.
type Options struct {
	Fuzz        int // search recorded position +/- this many lines
	MaxBytes    int
	MaxHunks    int
	MaxDistance int
}

// Diff renders the shortest unified diff a -> b.
func Diff(a, b []byte, context int, opt Options) ([]byte, error) {
	la, lb := lines.Split(a), lines.Split(b)
	s, err := edit.Diff(la, lb, edit.Options{MaxDistance: opt.MaxDistance})
	if err != nil {
		return nil, ErrTooDifferent
	}
	hs := hunk.Group(s, context)
	oldNoNL := len(la) > 0 && lines.NoNewline(la[len(la)-1])
	newNoNL := len(lb) > 0 && lines.NoNewline(lb[len(lb)-1])
	return udiff.Render("a", "b", hs, oldNoNL, newNoNL), nil
}

// Apply applies patch text to src; on any hunk failure src is returned
// byte-for-byte unchanged with an *ApplyError.
func Apply(src, data []byte, opt Options) ([]byte, error) {
	p, err := udiff.ParseLimited(data, opt.MaxBytes, opt.MaxHunks)
	if err != nil {
		return src, &ApplyError{Hunk: -1, Category: ErrFormat, Reason: err.Error()}
	}
	cur := lines.Split(src)
	out, err := applyHunks(cur, p.Hunks, opt.Fuzz, false)
	if err != nil {
		return src, err
	}
	return lines.Join(out), nil
}

// Reverse applies the inverse patch, transforming new text back to old.
func Reverse(src, data []byte, opt Options) ([]byte, error) {
	p, err := udiff.ParseLimited(data, opt.MaxBytes, opt.MaxHunks)
	if err != nil {
		return src, &ApplyError{Hunk: -1, Category: ErrFormat, Reason: err.Error()}
	}
	cur := lines.Split(src)
	out, err := applyHunks(cur, p.Hunks, opt.Fuzz, true)
	if err != nil {
		return src, err
	}
	return lines.Join(out), nil
}

func applyHunks(cur []lines.Line, hs []hunk.Hunk, fuzz int, reverse bool) ([]lines.Line, error) {
	shift := 0
	for i, h := range hs {
		rec := h.OldStart - 1 + shift
		if reverse {
			rec = h.NewStart - 1 + shift
		}
		pos, d, ok := locate(cur, h, rec, fuzz, reverse)
		if !ok {
			cat := ErrContext
			if d > fuzz {
				cat = ErrOutOfFuzz
			}
			return nil, &ApplyError{Hunk: i, Category: cat, Reason: "no matching context"}
		}
		var repl []lines.Line
		var oldCnt int
		for _, op := range h.Ops {
			if reverse {
				switch op.Kind {
				case edit.Equal:
					repl = append(repl, op.Line)
					oldCnt++
				case edit.Insert:
					repl = append(repl, op.Line)
					oldCnt++
				case edit.Delete:
				}
			} else {
				switch op.Kind {
				case edit.Equal, edit.Insert:
					repl = append(repl, op.Line)
				}
				if op.Kind != edit.Insert {
					oldCnt++
				}
			}
		}
		cur = append(cur[:pos:pos], append(repl, cur[pos+oldCnt:]...)...)
		shift += d
	}
	return cur, nil
}

// locate searches positions nearest rec (ties toward the front) where the
// hunk's old-side lines match exactly.
func locate(cur []lines.Line, h hunk.Hunk, rec, fuzz int, reverse bool) (int, int, bool) {
	want := oldSide(h, reverse)
	bestPos, bestDist := -1, 0
	lo := rec - fuzz
	if lo < 0 {
		lo = 0
	}
	hi := rec + fuzz
	if hi > len(cur) {
		hi = len(cur)
	}
	for p := lo; p <= hi; p++ {
		if p+len(want) > len(cur) {
			continue
		}
		match := true
		for i, w := range want {
			if !cur[p+i].Equal(w) {
				match = false
				break
			}
		}
		if match {
			d := p - rec
			if d < 0 {
				d = -d
			}
			if bestPos < 0 || d < bestDist {
				bestPos, bestDist = p, d
			}
		}
	}
	if bestPos < 0 {
		d := fuzz + 1
		return 0, d, false
	}
	return bestPos, bestDist, true
}

func oldSide(h hunk.Hunk, reverse bool) []lines.Line {
	var out []lines.Line
	for _, op := range h.Ops {
		if reverse {
			switch op.Kind {
			case edit.Equal, edit.Insert:
				out = append(out, op.Line)
			}
		} else if op.Kind != edit.Insert {
			out = append(out, op.Line)
		}
	}
	return out
}
