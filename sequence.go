package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Entry is one element of a Sequence: an immutable sort key plus the
// opaque value it orders.
type Entry struct {
	Key   string
	Value string
}

// Sequence is an ordered list of elements addressed by sort keys. It is
// safe for concurrent use. The zero value is not valid; use NewSequence.
type Sequence struct {
	mu      sync.RWMutex
	gen     Generator
	maxLen  int
	entries []Entry // always sorted by Key, keys unique
}

// NewSequence returns a Sequence using gen (nil selects the default
// LexGenerator) and the given maximum key length (<= 0 selects
// DefaultMaxKeyLen).
func NewSequence(gen Generator, maxLen int) *Sequence {
	if gen == nil {
		gen = NewLexGenerator()
	}
	if maxLen <= 0 {
		maxLen = DefaultMaxKeyLen
	}
	return &Sequence{gen: gen, maxLen: maxLen}
}

// Insert places value between the neighbor keys left and right and
// returns the new key. An empty string means "no neighbor" on that side,
// so Insert("", "", v) seeds an empty sequence.
//
// If the generated key is already taken (concurrent inserts into the same
// gap), the gap is narrowed deterministically by comparing values: a
// smaller value retried against the left side, a larger one against the
// right. The final order therefore depends only on the values, never on
// goroutine scheduling.
func (s *Sequence) Insert(left, right, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		key, err := s.gen.Between(left, right, s.maxLen)
		if err != nil {
			return "", err
		}
		pos, found := s.find(key)
		if !found {
			s.entries = append(s.entries, Entry{})
			copy(s.entries[pos+1:], s.entries[pos:])
			s.entries[pos] = Entry{Key: key, Value: value}
			return key, nil
		}
		switch {
		case value < s.entries[pos].Value:
			right = key
		case value > s.entries[pos].Value:
			left = key
		default:
			return "", fmt.Errorf("%w: %q", ErrDuplicateValue, value)
		}
	}
}

// Snapshot returns a copy of the current entries in key order. Readers
// always observe a consistent generation: either all keys from before a
// Rebalance or all keys from after it, never a mix.
func (s *Sequence) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Len reports the number of elements.
func (s *Sequence) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// LongestKey reports the length of the longest current key and how many
// bytes remain before the configured maximum is reached.
func (s *Sequence) LongestKey() (length, remaining int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if len(e.Key) > length {
			length = len(e.Key)
		}
	}
	return length, s.maxLen - length
}

// SelfCheck verifies that every key is valid and that keys are strictly
// increasing with no duplicates. It returns nil when the sequence is
// healthy and is safe to call from tests at any time.
func (s *Sequence) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, e := range s.entries {
		if err := validateKey(e.Key); err != nil {
			return err
		}
		if e.Key == "" {
			return fmt.Errorf("entry %d: empty key", i)
		}
		if e.Key[len(e.Key)-1] == firstChar {
			return fmt.Errorf("entry %d: key %q ends with reserved %q", i, e.Key, firstChar)
		}
		if len(e.Key) > s.maxLen {
			return fmt.Errorf("entry %d: key %q exceeds max length %d", i, e.Key, s.maxLen)
		}
		if i > 0 && s.entries[i-1].Key >= e.Key {
			return fmt.Errorf("entries %d,%d: keys %q, %q not strictly increasing",
				i-1, i, s.entries[i-1].Key, e.Key)
		}
	}
	return nil
}

// find locates key by binary search, returning its insertion position
// and whether an entry with exactly that key exists.
func (s *Sequence) find(key string) (pos int, found bool) {
	pos = sort.Search(len(s.entries), func(i int) bool {
		return s.entries[i].Key >= key
	})
	if pos < len(s.entries) && s.entries[pos].Key == key {
		return pos, true
	}
	return pos, false
}
