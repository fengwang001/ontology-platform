// Package srvpick implements a weighted DNS SRV record selector with
// TTL-based expiration and exponential failure cooldown, following the
// priority/weight selection rules of RFC 2782.
package srvpick

import (
	"errors"
	"sort"
	"sync"
)

const (
	// MaxTime is the largest accepted value for any "now" timestamp.
	MaxTime = int64(1_000_000_000_000_000) // 1e15
	// MaxTTL is the largest accepted TTL.
	MaxTTL = int64(1_000_000_000) // 1e9
	// MaxCooldown is the largest accepted cooldown / CoolCap / Wr value.
	MaxCooldown = int64(1_000_000_000) // 1e9
	// maxPort is the largest valid port number.
	maxPort = 65535
	// maxUint16 is the largest valid priority / weight value.
	maxUint16 = 65535
	// maxShift caps the exponent of the exponential backoff at 30.
	maxShift = 30
)

// Rejection reasons returned by Selector operations. They are mutually
// distinguishable so callers can tell exactly why an operation failed.
var (
	// ErrInvalidConfig is returned by New when Cap < 1 or CoolCap / Wr
	// fall outside [1, 1e9].
	ErrInvalidConfig = errors.New("srvpick: invalid config")
	// ErrInvalidParam is returned when a non-time argument is out of range.
	ErrInvalidParam = errors.New("srvpick: invalid parameter")
	// ErrInvalidTime is returned when now < 0 or now > 1e15.
	ErrInvalidTime = errors.New("srvpick: invalid time")
	// ErrFull is returned by Add when the selector is at capacity and no
	// expired record could be evicted for a new identity.
	ErrFull = errors.New("srvpick: capacity full")
	// ErrNotFound is returned when no record exists for the identity.
	ErrNotFound = errors.New("srvpick: record not found")
	// ErrExpired is returned when the record exists but is already
	// expired at the given time.
	ErrExpired = errors.New("srvpick: record expired")
	// ErrNoAvailable is returned by Pick when no record is usable.
	ErrNoAvailable = errors.New("srvpick: no available record")
)

// record is a single stored SRV record.
type record struct {
	target   string
	port     int
	priority int
	weight   int
	// expireAt is the registration time plus TTL; the record is usable
	// only while now < expireAt.
	expireAt int64
	// seq is the registration sequence number, assigned from 1 upwards
	// at first registration and kept across updates.
	seq uint64
	// cooldownUntil only ever increases for a stored record.
	cooldownUntil int64
	// failures counts consecutive failures (f).
	failures int
	// lastFail is the time of the most recent Failure call.
	lastFail    int64
	hasLastFail bool
}

// key identifies a record by (target, port).
type key struct {
	target string
	port   int
}

// Selector is a concurrency-safe weighted SRV record selector. All
// methods are equivalent to some serial execution order.
type Selector struct {
	mu       sync.Mutex
	capacity int
	coolCap  int64
	wr       int64
	records  map[key]*record
	nextSeq  uint64
}

// New creates a Selector. Cap must be >= 1, and CoolCap and Wr must be
// in [1, 1e9]; otherwise the whole configuration is rejected with
// ErrInvalidConfig.
func New(capacity int, coolCap, wr int64) (*Selector, error) {
	if capacity < 1 ||
		coolCap < 1 || coolCap > MaxCooldown ||
		wr < 1 || wr > MaxCooldown {
		return nil, ErrInvalidConfig
	}
	return &Selector{
		capacity: capacity,
		coolCap:  coolCap,
		wr:       wr,
		records:  make(map[key]*record),
	}, nil
}

func validTime(now int64) bool {
	return now >= 0 && now <= MaxTime
}

// Add registers or updates a record identified by (target, port).
//
// On first registration a strictly increasing sequence number (from 1)
// is assigned. Re-registering an existing identity is an update: it
// overwrites priority and weight and sets the expiry to now+ttl, while
// keeping the original sequence number, cooldown deadline, consecutive
// failure count and last failure time.
//
// Rejection reasons, checked in this order: ErrInvalidParam (empty
// target, port/priority/weight out of range, ttl out of [1, 1e9]),
// ErrInvalidTime, ErrFull. When the store is full and the identity is
// new, all records expired at now are evicted first; only if it is
// still full afterwards is ErrFull reported. A rejected call changes
// nothing.
func (s *Selector) Add(target string, port, priority, weight int, ttl, now int64) error {
	if target == "" ||
		port < 1 || port > maxPort ||
		priority < 0 || priority > maxUint16 ||
		weight < 0 || weight > maxUint16 ||
		ttl < 1 || ttl > MaxTTL {
		return ErrInvalidParam
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{target: target, port: port}
	if rec, ok := s.records[k]; ok {
		rec.priority = priority
		rec.weight = weight
		rec.expireAt = now + ttl
		return nil
	}
	if len(s.records) >= s.capacity {
		s.evictLocked(now)
		if len(s.records) >= s.capacity {
			return ErrFull
		}
	}
	s.nextSeq++
	s.records[k] = &record{
		target:   target,
		port:     port,
		priority: priority,
		weight:   weight,
		expireAt: now + ttl,
		seq:      s.nextSeq,
	}
	return nil
}

// Failure reports a failure of the record identified by (target, port).
//
// If there is no previous failure time, or now >= lastFail+Wr, the
// consecutive failure count f is reset to 0 first. Then f is
// incremented, the last failure time is set to now, the effective
// cooldown is min(CoolCap, cooldown * 2^min(f-1, 30)), and the cooldown
// deadline becomes max(old deadline, now + effective cooldown).
//
// Rejection reasons, checked in this order: ErrInvalidParam (empty
// target, cooldown out of [1, 1e9]), ErrInvalidTime, ErrNotFound,
// ErrExpired (now >= expiry).
func (s *Selector) Failure(target string, port int, cooldown, now int64) error {
	if target == "" || cooldown < 1 || cooldown > MaxCooldown {
		return ErrInvalidParam
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key{target: target, port: port}]
	if !ok {
		return ErrNotFound
	}
	if now >= rec.expireAt {
		return ErrExpired
	}
	if !rec.hasLastFail || now >= rec.lastFail+s.wr {
		rec.failures = 0
	}
	rec.failures++
	rec.lastFail = now
	rec.hasLastFail = true
	shift := rec.failures - 1
	if shift > maxShift {
		shift = maxShift
	}
	eff := cooldown << uint(shift)
	if eff > s.coolCap {
		eff = s.coolCap
	}
	if until := now + eff; until > rec.cooldownUntil {
		rec.cooldownUntil = until
	}
	return nil
}

// Success reports a success of the record identified by (target, port).
// It only resets the consecutive failure count f to 0; the cooldown
// deadline and the last failure time are left unchanged.
//
// Rejection reasons, checked in this order: ErrInvalidParam (empty
// target), ErrInvalidTime, ErrNotFound, ErrExpired.
func (s *Selector) Success(target string, port int, now int64) error {
	if target == "" {
		return ErrInvalidParam
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key{target: target, port: port}]
	if !ok {
		return ErrNotFound
	}
	if now >= rec.expireAt {
		return ErrExpired
	}
	rec.failures = 0
	return nil
}

// Pick selects a record following RFC 2782: among records usable at now
// (now < expiry and now >= cooldown deadline) the lowest priority group
// is chosen. Inside the group, zero-weight records sorted by sequence
// number come first, followed by positive-weight records sorted by
// sequence number. With S the group weight sum and r1 = r mod (S+1),
// weights are accumulated in that order and the first record whose
// cumulative weight is >= r1 wins.
//
// Pick is deterministic and does not mutate any state. It returns
// ErrInvalidTime for an invalid timestamp and ErrNoAvailable when no
// record is usable.
func (s *Selector) Pick(r uint64, now int64) (string, int, error) {
	if !validTime(now) {
		return "", 0, ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var group []*record
	minPriority := 0
	for _, rec := range s.records {
		if now >= rec.expireAt || now < rec.cooldownUntil {
			continue
		}
		if len(group) == 0 || rec.priority < minPriority {
			minPriority = rec.priority
			group = group[:0]
		}
		if rec.priority == minPriority {
			group = append(group, rec)
		}
	}
	if len(group) == 0 {
		return "", 0, ErrNoAvailable
	}
	sort.Slice(group, func(i, j int) bool {
		zi, zj := group[i].weight == 0, group[j].weight == 0
		if zi != zj {
			return zi
		}
		return group[i].seq < group[j].seq
	})
	var sum uint64
	for _, rec := range group {
		sum += uint64(rec.weight)
	}
	r1 := r % (sum + 1)
	var cum uint64
	for _, rec := range group {
		cum += uint64(rec.weight)
		if cum >= r1 {
			return rec.target, rec.port, nil
		}
	}
	// Unreachable: the final cumulative value is sum >= r1.
	return "", 0, ErrNoAvailable
}

// Purge deletes every record whose expiry is not after now and returns
// the number of deleted records. A deleted identity registered again
// later receives a fresh sequence number.
func (s *Selector) Purge(now int64) (int, error) {
	if !validTime(now) {
		return 0, ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictLocked(now), nil
}

// evictLocked removes records with expireAt <= now and returns the
// number removed. The caller must hold s.mu.
func (s *Selector) evictLocked(now int64) int {
	removed := 0
	for k, rec := range s.records {
		if rec.expireAt <= now {
			delete(s.records, k)
			removed++
		}
	}
	return removed
}
