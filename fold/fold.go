package fold

import (
	"strings"
	"sync/atomic"
	"unicode"
)

type Folder struct {
	lastRunes atomic.Int64
}

func New() *Folder { return &Folder{} }

func Fold(s string) string {
	return New().Fold(s)
}

func (f *Folder) Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var n int64
	for _, r := range s {
		b.WriteRune(canonical(r))
		n++
	}
	if f != nil {
		f.lastRunes.Store(n)
	}
	return b.String()
}

func canonical(r rune) rune {
	best := r
	for cur := unicode.SimpleFold(r); cur != r; cur = unicode.SimpleFold(cur) {
		if unicode.IsLower(cur) && (!unicode.IsLower(best) || cur < best) {
			best = cur
		}
	}
	if unicode.IsLower(best) {
		return best
	}
	return r
}
