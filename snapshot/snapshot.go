// Package snapshot freezes object state into an immutable, byte-deterministic view.
// Validation hooks receive a *Snapshot instead of a live object, so they have no
// write surface and every hook in a phase observes exactly the same bytes.
package snapshot

import (
	"encoding/binary"
	"sort"
)

// Snapshot is a frozen object state. It exposes only read accessors.
type Snapshot struct {
	typ   string
	id    string
	props map[string]string
	raw   []byte
}

// Freeze builds an immutable snapshot. Properties are copied and serialized in
// key-sorted order, so two states with equal content yield identical bytes
// regardless of map iteration or insertion order.
func Freeze(typ, id string, props map[string]string) *Snapshot {
	cp := make(map[string]string, len(props))
	for k, v := range props {
		cp[k] = v
	}
	keys := make([]string, 0, len(cp))
	for k := range cp {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var raw []byte
	raw = appendBytes(raw, typ)
	raw = appendBytes(raw, id)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(keys)))
	raw = append(raw, n[:]...)
	for _, k := range keys {
		raw = appendBytes(raw, k)
		raw = appendBytes(raw, cp[k])
	}
	return &Snapshot{typ: typ, id: id, props: cp, raw: raw}
}

func appendBytes(dst []byte, s string) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...)
}

// Type returns the object type.
func (s *Snapshot) Type() string { return s.typ }

// ID returns the object id.
func (s *Snapshot) ID() string { return s.id }

// Get returns the property value and whether it exists.
func (s *Snapshot) Get(name string) (string, bool) {
	v, ok := s.props[name]
	return v, ok
}

// Bytes returns the deterministic frozen encoding.
func (s *Snapshot) Bytes() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

// Equal reports whether two snapshots have identical frozen content.
func (s *Snapshot) Equal(other *Snapshot) bool {
	return string(s.raw) == string(other.raw)
}
