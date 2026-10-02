// Package dnssrv implements an RFC 2782 style weighted SRV record selector
// with TTL expiry eviction and exponential failure cooldown.
package dnssrv

import (
	"errors"
	"sort"
	"sync"
)

// Errors returned by selector operations. They are returned directly, so
// callers can distinguish rejection reasons with errors.Is.
var (
	ErrInvalidConfig = errors.New("dnssrv: invalid config")
	ErrInvalidArg    = errors.New("dnssrv: invalid argument")
	ErrInvalidTime   = errors.New("dnssrv: invalid time")
	ErrFull          = errors.New("dnssrv: selector full")
	ErrNotFound      = errors.New("dnssrv: record not found")
	ErrExpired       = errors.New("dnssrv: record expired")
	ErrNoAvailable   = errors.New("dnssrv: no available record")
)

const (
	maxBound    = int64(1_000_000_000)
	maxTime     = int64(1_000_000_000_000_000)
	maxShift    = 30
	maxPort     = 65535
	maxPriority = 65535
	maxWeight   = 65535
)

// Selector stores SRV records keyed by (target, port). It is safe for
// concurrent use by multiple goroutines.
type Selector struct {
	mu      sync.Mutex
	cap     int
	coolCap int64
	wr      int64
	nextID  int64
	recs    map[id]*record
}

type id struct {
	target string
	port   int
}

type record struct {
	key       id
	priority  int
	weight    int
	expire    int64
	regID     int64 // registration sequence number, starting at 1
	coolUntil int64
	f         int   // consecutive failure count
	lf        int64 // last failure time, valid only when hasLF is true
	hasLF     bool
}

// New creates a Selector. cap is the record capacity (>=1), coolCap is the
// effective cooldown ceiling in [1,1e9], wr is the failure silence window in
// [1,1e9].
func New(capacity int, coolCap, wr int64) (*Selector, error) {
	if capacity < 1 || coolCap < 1 || coolCap > maxBound || wr < 1 || wr > maxBound {
		return nil, ErrInvalidConfig
	}
	return &Selector{
		cap:     capacity,
		coolCap: coolCap,
		wr:      wr,
		nextID:  1,
		recs:    make(map[id]*record),
	}, nil
}

// Add registers or updates a record.
func (s *Selector) Add(target string, port, priority, weight int, ttl, now int64) error {
	if target == "" || port < 1 || port > maxPort ||
		priority < 0 || priority > maxPriority ||
		weight < 0 || weight > maxWeight ||
		ttl < 1 || ttl > maxBound {
		return ErrInvalidArg
	}
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := id{target: target, port: port}
	if rec := s.recs[key]; rec != nil {
		// Update: overwrite priority/weight/expire only; keep regID,
		// cooldown deadline, failure count and last failure time.
		rec.priority = priority
		rec.weight = weight
		rec.expire = now + ttl
		return nil
	}
	if len(s.recs) >= s.cap {
		// New identifier and no free slot: evict expired records first.
		s.purgeLocked(now)
		if len(s.recs) >= s.cap {
			return ErrFull
		}
	}
	s.recs[key] = &record{
		key:       key,
		priority:  priority,
		weight:    weight,
		expire:    now + ttl,
		regID:     s.nextID,
		coolUntil: 0,
		f:         0,
	}
	s.nextID++
	return nil
}

// Failure reports a failed attempt against a record.
func (s *Selector) Failure(target string, port int, cooldown, now int64) error {
	if target == "" || cooldown < 1 || cooldown > maxBound {
		return ErrInvalidArg
	}
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.recs[id{target: target, port: port}]
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expire {
		return ErrExpired
	}

	if !rec.hasLF || now >= rec.lf+s.wr {
		rec.f = 0
	}
	rec.f++
	rec.lf = now
	rec.hasLF = true

	shift := rec.f - 1
	if shift > maxShift {
		shift = maxShift
	}
	effective := cooldown * (int64(1) << uint(shift))
	if effective > s.coolCap {
		effective = s.coolCap
	}
	deadline := now + effective
	if deadline > rec.coolUntil {
		rec.coolUntil = deadline
	}
	return nil
}

// Success reports a successful attempt, clearing the failure counter only.
func (s *Selector) Success(target string, port int, now int64) error {
	if target == "" {
		return ErrInvalidArg
	}
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.recs[id{target: target, port: port}]
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expire {
		return ErrExpired
	}
	rec.f = 0
	return nil
}

// Pick chooses an available record using random number r.
func (s *Selector) Pick(r uint64, now int64) (target string, port int, err error) {
	if now < 0 || now > maxTime {
		return "", 0, ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	groupPriority := -1
	var group []*record
	for _, rec := range s.recs {
		if !available(rec, now) {
			continue
		}
		if groupPriority == -1 || rec.priority < groupPriority {
			groupPriority = rec.priority
		}
	}
	if groupPriority == -1 {
		return "", 0, ErrNoAvailable
	}
	for _, rec := range s.recs {
		if available(rec, now) && rec.priority == groupPriority {
			group = append(group, rec)
		}
	}

	// Within the group: weight-0 records first (ascending regID), then
	// weight>0 records (ascending regID).
	sort.SliceStable(group, func(i, j int) bool {
		ri, rj := group[i], group[j]
		zi, zj := ri.weight == 0, rj.weight == 0
		if zi != zj {
			return zi
		}
		return ri.regID < rj.regID
	})

	var sum int64
	for _, rec := range group {
		sum += int64(rec.weight)
	}
	r1 := int64(r % (uint64(sum) + 1))

	var cumulative int64
	for _, rec := range group {
		cumulative += int64(rec.weight)
		if cumulative >= r1 {
			return rec.key.target, rec.key.port, nil
		}
	}
	// Unreachable: a non-empty group always reaches cumulative == sum >= r1.
	panic("dnssrv: internal selection error")
}

// Purge removes records whose expiry is <= now and returns the removed count.
func (s *Selector) Purge(now int64) (int, error) {
	if now < 0 || now > maxTime {
		return 0, ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purgeLocked(now), nil
}

func available(rec *record, now int64) bool {
	return now < rec.expire && now >= rec.coolUntil
}

func (s *Selector) purgeLocked(now int64) int {
	removed := 0
	for key, rec := range s.recs {
		if rec.expire <= now {
			delete(s.recs, key)
			removed++
		}
	}
	return removed
}
