// Package auditlog implements a forward-secure, key-evolving append-only
// audit log and a single-pass checkpoint verifier.
//
// The writer holds exactly one uint64 key at any time. Every successful
// Append or Seal advances the key with the injected evolve function; every
// successful Rekey advances it with the injected rekey function. Entries are
// authenticated with the injected mac function. A verifier holding one or
// more checkpoint (index, key) pairs verifies the whole segment at or after
// the smallest checkpoint index in a single forward pass.
package auditlog

import "sync"

// Entry kinds.
const (
	TypData  = 0 // data record
	TypSeal  = 1 // seal record
	TypRekey = 2 // rekey record
)

const (
	minCap     = 2
	maxCap     = 1_000_000
	maxMaxData = 4096
	maxTs      = 1_000_000_000_000_000
)

// EvolveFunc maps a record key to the key of the next index.
type EvolveFunc func(k uint64) uint64

// RekeyFunc derives a fresh key for a rekey record at index i.
type RekeyFunc func(k uint64, i int64) uint64

// MacFunc computes the authenticator of an entry.
type MacFunc func(k uint64, i int64, typ int32, ts int64, data []byte) uint64

// Entry is one log record. Index is the record sequence number, Typ is the
// entry kind, Ts is its timestamp, Data is its payload and Tag is its
// authenticator.
type Entry struct {
	Index int64
	Typ   int32
	Ts    int64
	Data  []byte
	Tag   uint64
}

// Log is a concurrency-safe forward-secure audit log.
//
// The zero value is not usable; create one with New.
type Log struct {
	evolve  EvolveFunc
	rekey   RekeyFunc
	mac     MacFunc
	cap     int
	maxData int

	mu      sync.RWMutex
	entries []*Entry
	ki      uint64
	nextI   int64
	lastTs  int64
	sealed  bool
}

// New creates a log with initial key k0, total entry capacity cap (including
// the mandatory seal record) and per-record data limit maxData. k0, cap and
// maxData are validated and the three deterministic functions are required.
func New(k0 uint64, cap int, maxData int, evolve EvolveFunc, rekey RekeyFunc, mac MacFunc) (*Log, error) {
	if cap < minCap || cap > maxCap || maxData < 0 || maxData > maxMaxData || evolve == nil || rekey == nil || mac == nil {
		return nil, ErrInvalidConfig
	}
	return &Log{
		evolve:  evolve,
		rekey:   rekey,
		mac:     mac,
		cap:     cap,
		maxData: maxData,
		entries: make([]*Entry, 0, cap),
		ki:      k0,
	}, nil
}
