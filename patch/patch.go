// Package patch applies parsed unified-diff hunks to text with bounded
// offset search and all-or-nothing semantics, and provides a concurrent
// multi-document store with versioning and a commit log.
package patch

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

var ErrContext = errors.New("patch: context mismatch")
var ErrOffset = errors.New("patch: offset out of range")
var ErrLimit = errors.New("patch: resource limit exceeded")

// Apply applies hunks to src; fuzz bounds the offset search in lines.
// On any hunk failure it returns a nil result and src is untouched.
func Apply(src []byte, hs []hunk.Hunk, fuzz int) ([]byte, error) {
	return apply(src, hs, fuzz, false)
}

// Reverse applies hunks backwards, turning new text back into old.
func Reverse(src []byte, hs []hunk.Hunk, fuzz int) ([]byte, error) {
	return apply(src, hs, fuzz, true)
}

func apply(src []byte, hs []hunk.Hunk, fuzz int, rev bool) ([]byte, error) {
	srcLines := lines.Split(src)
	out, pos, delta := make([]string, 0, len(srcLines)), 0, 0
	for i, h := range hs {
		var old, new []string
		start, count := h.OldStart, h.OldCount
		if rev {
			start, count = h.NewStart, h.NewCount
		}
		for _, l := range h.Lines {
			del, add := l.Kind == '-', l.Kind == '+'
			if rev {
				del, add = add, del
			}
			if l.Kind == ' ' || del {
				old = append(old, l.Text)
			}
			if l.Kind == ' ' || add {
				new = append(new, l.Text)
			}
		}
		guess := start + delta
		if count > 0 {
			guess--
		}
		at, anywhere := -1, false
		for j := pos; j+len(old) <= len(srcLines); j++ {
			if !slices.Equal(srcLines[j:j+len(old)], old) {
				continue
			}
			anywhere = true
			if d := abs(j - guess); d <= fuzz && (at < 0 || d < abs(at-guess)) {
				at = j
			}
		}
		if at < 0 {
			err := ErrContext
			if anywhere {
				err = ErrOffset
			}
			return nil, fmt.Errorf("hunk %d: %w", i+1, err)
		}
		out = append(out, srcLines[pos:at]...)
		out = append(out, new...)
		pos = at + len(old)
		delta += (at - guess) + len(new) - len(old)
	}
	return lines.Join(append(out, srcLines[pos:]...)), nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Options configures a Store; zero values mean fuzz 0 and no limits.
type Options struct {
	Fuzz     int
	MaxBytes int
	MaxHunks int
}

// Store holds versioned documents and applies patches atomically.
type Store struct {
	mu   sync.Mutex
	opts Options
	docs map[string][]byte
	vers map[string]int
	log  [][]byte
}

func NewStore(opts Options) *Store {
	return &Store{opts: opts, docs: map[string][]byte{}, vers: map[string]int{}}
}

func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name], s.vers[name] = text, 0
}

// Apply parses and applies a patch atomically; on success the version
// increments and the patch is appended to the commit log.
func (s *Store) Apply(name string, p []byte) error {
	if s.opts.MaxBytes > 0 && len(p) > s.opts.MaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrLimit, len(p))
	}
	hs, err := udiff.Parse(p)
	if err != nil {
		return err
	}
	if s.opts.MaxHunks > 0 && len(hs) > s.opts.MaxHunks {
		return fmt.Errorf("%w: %d hunks", ErrLimit, len(hs))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := Apply(s.docs[name], hs, s.opts.Fuzz)
	if err != nil {
		return err
	}
	s.docs[name] = out
	s.vers[name]++
	s.log = append(s.log, p)
	return nil
}

func (s *Store) Text(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[name], s.vers[name]
}

func (s *Store) Log() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}
