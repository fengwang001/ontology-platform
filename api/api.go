// Package api is the public, goroutine-safe facade of the autocomplete component.
package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"ontology/rank"
	"ontology/trie"
)

// Distinguishable sentinel errors for rejected operations.
var (
	ErrEmpty    = errors.New("empty string")
	ErrNotFound = errors.New("string not found")
	ErrBadK     = errors.New("k must be >= 1")
)

// UTF8Error reports invalid UTF-8 at byte Offset.
type UTF8Error struct{ Offset int }

func (e *UTF8Error) Error() string { return fmt.Sprintf("invalid UTF-8 at byte offset %d", e.Offset) }

func check(s string) error {
	if s == "" {
		return ErrEmpty
	}
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			return &UTF8Error{Offset: i}
		}
		i += w
	}
	return nil
}

// Trie is the public handle. All methods are safe for concurrent use.
type Trie struct {
	mu sync.RWMutex
	tr *trie.Trie
}

func New() *Trie { return &Trie{tr: trie.New()} }

func (a *Trie) Insert(s string, f int) error {
	if err := check(s); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tr.Insert(s, f)
	return nil
}

func (a *Trie) Delete(s string) error {
	if err := check(s); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.tr.Delete(s) {
		return ErrNotFound
	}
	return nil
}

func (a *Trie) Complete(prefix string, k int) ([]string, error) {
	if err := check(prefix); err != nil {
		return nil, err
	}
	if k < 1 {
		return nil, ErrBadK
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return rank.TopK(a.tr, prefix, k), nil
}

func (a *Trie) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tr.Count()
}

// naive is the reference implementation: collect, sort, truncate.
func naive(ref map[string]int, prefix string, k int) []string {
	var c []string
	for s := range ref {
		if strings.HasPrefix(s, prefix) {
			c = append(c, s)
		}
	}
	sort.Slice(c, func(i, j int) bool {
		if ref[c[i]] != ref[c[j]] {
			return ref[c[i]] > ref[c[j]]
		}
		return c[i] < c[j]
	})
	return c[:min(len(c), k)]
}

// verifyAll checks invariants 1-3 of t against ref.
func verifyAll(t *Trie, ref map[string]int) error {
	for _, p := range []string{"a", "ap", "app", "b", "x"} {
		for k := 1; k <= len(ref)+1; k++ {
			got, err := t.Complete(p, k)
			if err != nil || fmt.Sprint(got) != fmt.Sprint(naive(ref, p, k)) {
				return fmt.Errorf("Complete(%q,%d)=%v err=%v", p, k, got, err)
			}
		}
	}
	if !t.tr.CheckRefs() || t.Count() != len(ref) {
		return fmt.Errorf("refs/count inconsistent: count=%d want %d", t.Count(), len(ref))
	}
	return nil
}

// SelfCheck runs a built-in operation sequence on a scratch instance and
// verifies all four invariants. The receiver's data is never touched.
func (a *Trie) SelfCheck() error {
	t := New()
	ref := map[string]int{"apricot": 5, "apple": 5, "app": 3, "application": 2, "apex": 4, "banana": 7}
	for s, f := range ref {
		if err := t.Insert(s, f); err != nil {
			return err
		}
	}
	for _, s := range []string{"", "app", "banana", "apex"} { // "" = verify only
		if s != "" {
			t.Delete(s)
			delete(ref, s)
		}
		if err := verifyAll(t, ref); err != nil {
			return err
		}
	}
	// Invariant 4: rejected operations leave no trace.
	if t.Insert("", 1) == nil || t.Delete("ghost") == nil {
		return errors.New("rejected op returned nil error")
	}
	if _, err := t.Complete("a", 0); err == nil || t.Count() != len(ref) {
		return errors.New("rejected op changed state or k<=0 accepted")
	}
	return verifyAll(t, ref)
}
