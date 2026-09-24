// Package api is the public façade over the tiered store: it enforces
// capacity and key/value limits with distinguishable sentinel errors and
// exposes a built-in self-check. It depends only on package store.
package api

import (
	"errors"

	"ontology/store"
)

// Sentinel errors for the five distinct, decidable failure modes.
var (
	ErrBadMemCap   = store.ErrBadMemCap // memCap is non-positive
	ErrEmptyKey    = errors.New("api: key must not be empty")
	ErrEmptyValue  = errors.New("api: value must not be empty")
	ErrKeyTooLong  = errors.New("api: key length exceeds maxKeyLen")
	ErrTooManyKeys = errors.New("api: distinct key count would exceed maxKeys")
)

// Store is the externally usable tiered state store.
type Store struct {
	st        *store.Store
	maxKeyLen int
	maxKeys   int
}

// New creates a store with a hot tier of memCap keys.
func New(memCap, maxKeyLen, maxKeys int) (*Store, error) {
	st, err := store.NewStore(memCap)
	if err != nil {
		return nil, err
	}
	return &Store{st: st, maxKeyLen: maxKeyLen, maxKeys: maxKeys}, nil
}

// Write validates fully before touching any state, so every rejection is
// atomic: memory, disk, timestamps and the disk-read counter are unchanged.
func (s *Store) Write(k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	if v == "" {
		return ErrEmptyValue
	}
	if len(k) > s.maxKeyLen {
		return ErrKeyTooLong
	}
	if !s.st.Has(k) && s.st.TotalKeys() >= s.maxKeys {
		return ErrTooManyKeys
	}
	s.st.Write(k, v)
	return nil
}

// Read returns the current value transparently, regardless of tier. A
// key never written returns ("", false).
func (s *Store) Read(k string) (string, bool) { return s.st.Read(k) }

// DiskReads reports how many Reads were served from the cold tier.
func (s *Store) DiskReads() int { return s.st.DiskReads() }

// SelfCheck runs the store-level invariants and, on a fresh bounded
// store, verifies each rejection returns its distinct sentinel and leaves
// the state untouched and still usable.
func (s *Store) SelfCheck() error {
	if err := s.st.SelfCheck(); err != nil {
		return err
	}
	if _, err := New(0, 8, 8); !errors.Is(err, ErrBadMemCap) {
		return errors.New("selfcheck: non-positive memCap not rejected")
	}
	t, err := New(2, 4, 3)
	if err != nil {
		return err
	}
	cases := []struct {
		k, v string
		want error
	}{
		{"", "v", ErrEmptyKey},
		{"k", "", ErrEmptyValue},
		{"longkey", "v", ErrKeyTooLong}, // len 7 > 4
	}
	for _, c := range cases {
		before := t.st.TotalKeys()
		if err := t.Write(c.k, c.v); !errors.Is(err, c.want) {
			return errors.New("selfcheck: wrong sentinel for " + c.want.Error())
		}
		if t.st.TotalKeys() != before || t.DiskReads() != 0 {
			return errors.New("selfcheck: rejected op changed state")
		}
	}
	for _, k := range []string{"a", "b", "c"} { // fill to maxKeys
		if err := t.Write(k, "1"); err != nil {
			return err
		}
	}
	if err := t.Write("d", "1"); !errors.Is(err, ErrTooManyKeys) {
		return errors.New("selfcheck: maxKeys overflow not rejected")
	}
	if err := t.Write("a", "2"); err != nil { // overwriting existing key stays allowed
		return errors.New("selfcheck: overwrite of existing key wrongly rejected")
	}
	if v, ok := t.Read("a"); !ok || v != "2" {
		return errors.New("selfcheck: store unusable after rejections")
	}
	return nil
}
