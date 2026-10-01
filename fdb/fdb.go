// Package fdb implements an Ethernet learning switch forwarding database
// (FDB) with an injected clock. It learns source addresses, forwards by
// destination address, ages out dynamic entries, protects static entries,
// and evicts the oldest dynamic entries when capacity is reached.
//
// All mutating methods take an explicit timestamp t (nanoseconds). Every
// mutating call validates its parameters, rejects a timestamp earlier than
// the last successful call (ErrClockBackward), purges expired dynamic
// entries, and only then applies its own logic. Rejected calls change no
// state, clock, or counters.
//
// The type is safe for concurrent use; results are equivalent to some
// serial order, and replaying the same call sequence reproduces identical
// return values and counters.
package fdb

import (
	"errors"
	"sync"
)

// MAC is a 6-byte Ethernet address. An address whose first byte has the
// least significant bit set is multicast (broadcast ff:ff:ff:ff:ff:ff
// included).
type MAC [6]byte

// IsMulticast reports whether m is a multicast (or broadcast) address.
func (m MAC) IsMulticast() bool { return m[0]&0x01 == 0x01 }

// Key is the FDB table key: (VLAN, MAC).
type Key struct {
	VLAN uint16
	MAC  MAC
}

// Counters accumulates event counts. The invariant
//
//	Len() + Evictions + Expired + Flushed + Overridden == Learned
//
// holds at all times.
type Counters struct {
	Learned       uint64 // dynamic entries created by source learning
	Expired       uint64 // dynamic entries removed by aging
	Moves         uint64 // dynamic entries whose port changed on re-learn
	Evictions     uint64 // dynamic entries evicted to make room at capacity
	Floods        uint64 // frames flooded (multicast dest or unknown dest)
	Filtered      uint64 // frames filtered (dest known on the ingress port)
	SecurityDrops uint64 // frames dropped by static-entry port protection
	Flushed       uint64 // dynamic entries removed by FlushPort
	Overridden    uint64 // dynamic entries replaced by AddStatic
}

var (
	// ErrPortOutOfRange is returned when a port number is not in [0, N).
	ErrPortOutOfRange = errors.New("fdb: port out of range")
	// ErrVLANOutOfRange is returned when a VLAN id is not in [1, 4094].
	ErrVLANOutOfRange = errors.New("fdb: vlan out of range")
	// ErrMulticastStaticMAC is returned when AddStatic is given a
	// multicast MAC address.
	ErrMulticastStaticMAC = errors.New("fdb: static entry mac must not be multicast")
	// ErrClockBackward is returned when a call carries a timestamp
	// earlier than the last successful call's timestamp.
	ErrClockBackward = errors.New("fdb: clock backward")
	// ErrInvalidConfig is returned by New for invalid construction
	// parameters.
	ErrInvalidConfig = errors.New("fdb: invalid config")
)

const (
	minVLAN = 1
	maxVLAN = 4094
)

type entry struct {
	port   int
	seen   int64
	static bool
}

// FDB is a learning switch forwarding database with an injected clock.
type FDB struct {
	mu       sync.Mutex
	numPorts int
	aging    int64
	capacity int

	entries  map[Key]*entry
	dynCount int // dynamic entries, including not-yet-purged expired ones

	last    int64
	hasLast bool

	ctr Counters
}

// New creates an FDB with numPorts ports (1..64, numbered 0..numPorts-1),
// aging time agingNS nanoseconds (must be positive), and dynamic entry
// capacity (must be positive; static entries do not occupy capacity).
func New(numPorts int, agingNS int64, capacity int) (*FDB, error) {
	if numPorts < 1 || numPorts > 64 || agingNS <= 0 || capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	return &FDB{
		numPorts: numPorts,
		aging:    agingNS,
		capacity: capacity,
		entries:  make(map[Key]*entry),
	}, nil
}

func (f *FDB) checkPort(p int) error {
	if p < 0 || p >= f.numPorts {
		return ErrPortOutOfRange
	}
	return nil
}

func (f *FDB) checkVLAN(v uint16) error {
	if v < minVLAN || v > maxVLAN {
		return ErrVLANOutOfRange
	}
	return nil
}

// checkClock rejects timestamps earlier than the last successful call.
func (f *FDB) checkClock(t int64) error {
	if f.hasLast && t < f.last {
		return ErrClockBackward
	}
	return nil
}

func (f *FDB) advanceClock(t int64) {
	f.last = t
	f.hasLast = true
}

// purge removes all expired dynamic entries at time t. A dynamic entry is
// expired when t - seen >= aging (left-closed).
func (f *FDB) purge(t int64) {
	for k, e := range f.entries {
		if !e.static && t-e.seen >= f.aging {
			delete(f.entries, k)
			f.dynCount--
			f.ctr.Expired++
		}
	}
}

// evictOldest removes the dynamic entry with the smallest seen timestamp;
// ties are broken by the (VLAN, MAC) key in byte order (VLAN first, as a
// 2-byte big-endian prefix, then MAC).
func (f *FDB) evictOldest() {
	var victim Key
	var victimEntry *entry
	first := true
	for k, e := range f.entries {
		if e.static {
			continue
		}
		if first || e.seen < victimEntry.seen ||
			(e.seen == victimEntry.seen && keyLess(k, victim)) {
			victim, victimEntry, first = k, e, false
		}
	}
	if !first {
		delete(f.entries, victim)
		f.dynCount--
		f.ctr.Evictions++
	}
}

// keyLess compares keys in byte order: VLAN as a big-endian 2-byte prefix
// (equivalent to numeric comparison), then MAC bytes.
func keyLess(a, b Key) bool {
	if a.VLAN != b.VLAN {
		return a.VLAN < b.VLAN
	}
	for i := 0; i < 6; i++ {
		if a.MAC[i] != b.MAC[i] {
			return a.MAC[i] < b.MAC[i]
		}
	}
	return false
}

// Frame processes an incoming frame on port p with source s, destination
// d, VLAN v at time t, and returns the sorted ascending list of egress
// ports. Processing order: validate parameters; purge expired dynamic
// entries; learn; forward.
func (f *FDB) Frame(p int, s, d MAC, v uint16, t int64) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.checkPort(p); err != nil {
		return nil, err
	}
	if err := f.checkVLAN(v); err != nil {
		return nil, err
	}
	if err := f.checkClock(t); err != nil {
		return nil, err
	}
	f.advanceClock(t)
	f.purge(t)

	// Learn: multicast sources are never learned but the frame is still
	// forwarded.
	if !s.IsMulticast() {
		k := Key{VLAN: v, MAC: s}
		if e, ok := f.entries[k]; ok {
			if e.static {
				// Static entries are never modified by learning. A
				// frame arriving on a different port is dropped.
				if e.port != p {
					f.ctr.SecurityDrops++
					return []int{}, nil
				}
			} else {
				if e.port != p {
					f.ctr.Moves++
				}
				e.port = p
				e.seen = t
			}
		} else {
			if f.dynCount == f.capacity {
				f.evictOldest()
			}
			f.entries[k] = &entry{port: p, seen: t}
			f.dynCount++
			f.ctr.Learned++
		}
	}

	// Forward.
	if d.IsMulticast() {
		f.ctr.Floods++
		return f.floodSet(p), nil
	}
	e, ok := f.entries[Key{VLAN: v, MAC: d}]
	if !ok {
		f.ctr.Floods++
		return f.floodSet(p), nil
	}
	if e.port == p {
		f.ctr.Filtered++
		return []int{}, nil
	}
	return []int{e.port}, nil
}

// floodSet returns all ports except p, in ascending order.
func (f *FDB) floodSet(p int) []int {
	out := make([]int, 0, f.numPorts-1)
	for port := 0; port < f.numPorts; port++ {
		if port != p {
			out = append(out, port)
		}
	}
	return out
}

// AddStatic installs or updates a static entry for (v, mac) on the given
// port at time t. mac must not be multicast. An existing dynamic entry
// with the same key is overridden (Overridden incremented); an existing
// static entry has its port changed. Static entries never expire and do
// not occupy dynamic capacity.
func (f *FDB) AddStatic(v uint16, mac MAC, port int, t int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.checkPort(port); err != nil {
		return err
	}
	if err := f.checkVLAN(v); err != nil {
		return err
	}
	if mac.IsMulticast() {
		return ErrMulticastStaticMAC
	}
	if err := f.checkClock(t); err != nil {
		return err
	}
	f.advanceClock(t)
	f.purge(t)

	k := Key{VLAN: v, MAC: mac}
	if e, ok := f.entries[k]; ok {
		if e.static {
			e.port = port
		} else {
			f.entries[k] = &entry{port: port, seen: t, static: true}
			f.dynCount--
			f.ctr.Overridden++
		}
	} else {
		f.entries[k] = &entry{port: port, seen: t, static: true}
	}
	return nil
}

// FlushPort removes all dynamic entries on the given port at time t and
// adds the number of removed entries to Flushed. Static entries are not
// affected.
func (f *FDB) FlushPort(port int, t int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.checkPort(port); err != nil {
		return err
	}
	if err := f.checkClock(t); err != nil {
		return err
	}
	f.advanceClock(t)
	f.purge(t)

	for k, e := range f.entries {
		if !e.static && e.port == port {
			delete(f.entries, k)
			f.dynCount--
			f.ctr.Flushed++
		}
	}
	return nil
}

// Lookup is a read-only query for (v, mac) at time t. An expired dynamic
// entry counts as a miss. Lookup changes no state, clock, or counters.
func (f *FDB) Lookup(v uint16, mac MAC, t int64) (port int, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, found := f.entries[Key{VLAN: v, MAC: mac}]
	if !found {
		return 0, false
	}
	if !e.static && t-e.seen >= f.aging {
		return 0, false
	}
	return e.port, true
}

// Len returns the number of dynamic entries currently stored, including
// expired ones that have not yet been purged.
func (f *FDB) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dynCount
}

// Counters returns a copy of the current counters.
func (f *FDB) Counters() Counters {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctr
}
