// Package lease implements an integer-address lease server with
// reservations (holds), confirmations, renewals, releases and rejections.
package lease

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// Sentinel errors returned by every operation. The error ordering in the
// package documentation matches the required rejection precedence.
var (
	ErrClockRolledBack  = errors.New("lease: clock rolled back")
	ErrEmptyClient      = errors.New("lease: client is empty")
	ErrAddressNotInPool = errors.New("lease: address not in pool")
	ErrPoolExhausted    = errors.New("lease: pool exhausted")
	ErrHeldByOther      = errors.New("lease: address held or reserved by another client")
	ErrNoReservation    = errors.New("lease: client has no valid reservation or lease")
	ErrNotHolder        = errors.New("lease: client is not the holder")
)

// Logger is the minimal logging surface used to record each operation's
// input, output and decision rationale.
type Logger interface {
	Printf(format string, args ...any)
}

// discardLogger drops all log output. It is used when Config.Logger is nil.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// DefaultLogger writes lease operation logs to w.
func DefaultLogger(w io.Writer) Logger {
	return log.New(w, "[lease] ", log.LstdFlags|log.Lmicroseconds)
}

// Config holds the server parameters.
type Config struct {
	Low         int64 // inclusive lower bound of the address pool
	High        int64 // inclusive upper bound of the address pool
	LeaseTime   int64 // L: lease duration
	ReserveTime int64 // H: reservation (hold) duration
	Quarantine  int64 // Q: rejection isolation duration
	Logger      Logger
}

// Server is the concurrency-safe lease server.
type Server struct {
	cfg Config
	log Logger

	mu        sync.Mutex
	addrs     map[int64]*state
	leaseOf   map[string]int64 // client -> address whose lease record names it
	reserveOf map[string]int64 // client -> address whose reservation names it
	lastHeld  map[string]int64 // client -> address it most recently held
	lastTick  int64
}

// state tracks the lifecycle of one address.
type state struct {
	leaseClient string
	leaseEnd    int64

	reserveClient string
	reserveEnd    int64

	// terminatedAt is the "lease termination time": the lease end for an
	// expired lease, the release time for a released lease, or the rejection
	// time when the holder rejected. Rejecting a reservation leaves it intact.
	terminatedAt int64
	everLeased   bool

	quarantineEnd int64
}

// New constructs a Server for the closed interval [Low, High].
func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = discardLogger{}
	}
	return &Server{
		cfg:       cfg,
		log:       logger,
		addrs:     make(map[int64]*state),
		leaseOf:   make(map[string]int64),
		reserveOf: make(map[string]int64),
		lastHeld:  make(map[string]int64),
	}
}

func (s *Server) get(addr int64) *state {
	st, ok := s.addrs[addr]
	if !ok {
		st = &state{}
		s.addrs[addr] = st
	}
	return st
}

func (st *state) leaseValid(now int64) bool {
	return st.leaseClient != "" && now < st.leaseEnd
}

func (st *state) reserveValid(now int64) bool {
	return st.reserveClient != "" && now < st.reserveEnd
}

func (st *state) quarantined(now int64) bool {
	return now < st.quarantineEnd
}

// free reports whether the address has no valid lease, no valid reservation
// and is not quarantined at time now.
func (st *state) free(now int64) bool {
	return !st.leaseValid(now) && !st.reserveValid(now) && !st.quarantined(now)
}

// sweep lazily retires expired lease/reservation records. An expired lease
// records its end as the lease termination time; an expired reservation only
// clears itself. Caller holds s.mu.
func (s *Server) sweep(st *state, now int64) {
	if st.leaseClient != "" && now >= st.leaseEnd {
		s.clearLease(st, st.leaseEnd)
	}
	if st.reserveClient != "" && now >= st.reserveEnd {
		s.clearReserve(st)
	}
}

func (s *Server) inPool(addr int64) bool {
	return s.cfg.Low <= addr && addr <= s.cfg.High
}

// checkClock validates monotonic time. Caller must hold s.mu.
func (s *Server) checkClock(now int64) error {
	if now < s.lastTick {
		return ErrClockRolledBack
	}
	return nil
}

// advance records a successful operation's time. Caller must hold s.mu.
func (s *Server) advance(now int64) {
	if now > s.lastTick {
		s.lastTick = now
	}
}

// clearLease removes the lease and captures its termination time.
// Caller holds s.mu.
func (s *Server) clearLease(st *state, end int64) {
	client := st.leaseClient
	st.terminatedAt = end
	st.everLeased = true
	st.leaseClient = ""
	st.leaseEnd = 0
	if client != "" {
		if addr, ok := s.leaseOf[client]; ok && s.addrs[addr] == st {
			delete(s.leaseOf, client)
		}
	}
}

// clearReserve removes a reservation. Caller holds s.mu.
func (s *Server) clearReserve(st *state) {
	client := st.reserveClient
	st.reserveClient = ""
	st.reserveEnd = 0
	if client != "" {
		if addr, ok := s.reserveOf[client]; ok && s.addrs[addr] == st {
			delete(s.reserveOf, client)
		}
	}
}

// setReserve records a new reservation and fixes stale back-pointers.
// Caller holds s.mu.
func (s *Server) setReserve(st *state, addr, now int64, client string) {
	if old, ok := s.reserveOf[client]; ok && old != addr {
		if oldSt := s.addrs[old]; oldSt != nil && oldSt.reserveClient == client {
			oldSt.reserveClient = ""
			oldSt.reserveEnd = 0
		}
		delete(s.reserveOf, client)
	}
	st.reserveClient = client
	st.reserveEnd = now + s.cfg.ReserveTime
	s.reserveOf[client] = addr
}

// bestFree returns the free address with the earliest lease termination time;
// addresses never leased rank earliest, ties break on the smaller address.
// The pool is scanned in ascending order. Caller holds s.mu.
func (s *Server) bestFree(now int64) (int64, bool) {
	var best int64
	var bestSt *state
	found := false
	for addr := s.cfg.Low; addr <= s.cfg.High; addr++ {
		st := s.get(addr)
		s.sweep(st, now)
		if !st.free(now) {
			continue
		}
		switch {
		case !found:
			best, bestSt, found = addr, st, true
		case !st.everLeased && bestSt.everLeased:
			best, bestSt = addr, st
		case st.everLeased == bestSt.everLeased && st.terminatedAt < bestSt.terminatedAt:
			best, bestSt = addr, st
		}
	}
	return best, found
}

func fmtReq(r *int64) string {
	if r == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *r)
}

// Discover reserves an address for the client following the fixed priority.
func (s *Server) Discover(now int64, client string, requested *int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	in := fmt.Sprintf("DISCOVER in={now:%d client:%q requested:%s}", now, client, fmtReq(requested))
	if err := s.checkClock(now); err != nil {
		s.log.Printf("%s -> ERR %v (last tick %d)", in, err, s.lastTick)
		return 0, err
	}
	if client == "" {
		s.log.Printf("%s -> ERR %v", in, ErrEmptyClient)
		return 0, ErrEmptyClient
	}
	if requested != nil && !s.inPool(*requested) {
		s.log.Printf("%s -> ERR %v (pool [%d,%d])", in, ErrAddressNotInPool, s.cfg.Low, s.cfg.High)
		return 0, ErrAddressNotInPool
	}

	// Rule 1: the client's existing valid lease wins; otherwise its existing
	// valid reservation is reused and its start is never refreshed.
	if addr, ok := s.leaseOf[client]; ok {
		if st := s.addrs[addr]; st != nil {
			s.sweep(st, now)
			if st.leaseValid(now) {
				s.advance(now)
				s.log.Printf("%s -> %d OK (rule 1: reuse valid lease until %d)", in, addr, st.leaseEnd)
				return addr, nil
			}
		}
	}
	if addr, ok := s.reserveOf[client]; ok {
		if st := s.addrs[addr]; st != nil {
			s.sweep(st, now)
			if st.reserveValid(now) {
				s.advance(now)
				s.log.Printf("%s -> %d OK (rule 1: keep reservation until %d, start not refreshed)", in, addr, st.reserveEnd)
				return addr, nil
			}
		}
	}

	// Rule 2: the address the client most recently held, if free.
	if addr, ok := s.lastHeld[client]; ok {
		if st := s.addrs[addr]; st != nil {
			s.sweep(st, now)
			if st.free(now) {
				s.setReserve(st, addr, now, client)
				s.advance(now)
				s.log.Printf("%s -> %d OK (rule 2: last-held address free, reserved [%d,%d))",
					in, addr, now, st.reserveEnd)
				return addr, nil
			}
		}
	}

	// Rule 3: the requested address when it lies in the pool and is free.
	if requested != nil {
		addr := *requested
		st := s.get(addr)
		s.sweep(st, now)
		if st.free(now) {
			s.setReserve(st, addr, now, client)
			s.advance(now)
			s.log.Printf("%s -> %d OK (rule 3: requested address free, reserved [%d,%d))",
				in, addr, now, st.reserveEnd)
			return addr, nil
		}
	}

	// Rule 4: free address with the earliest lease termination time.
	addr, ok := s.bestFree(now)
	if !ok {
		s.log.Printf("%s -> ERR %v (no free address in [%d,%d])", in, ErrPoolExhausted, s.cfg.Low, s.cfg.High)
		return 0, ErrPoolExhausted
	}
	st := s.get(addr)
	s.setReserve(st, addr, now, client)
	s.advance(now)
	term := "never leased (earliest)"
	if st.everLeased {
		term = fmt.Sprintf("lease terminated at %d (earliest)", st.terminatedAt)
	}
	s.log.Printf("%s -> %d OK (rule 4: %s, reserved [%d,%d))", in, addr, term, now, st.reserveEnd)
	return addr, nil
}

// Confirm turns the client's reservation (or renews its lease) into a lease.
func (s *Server) Confirm(now int64, client string, addr int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	in := fmt.Sprintf("CONFIRM in={now:%d client:%q addr:%d}", now, client, addr)
	if err := s.checkClock(now); err != nil {
		s.log.Printf("%s -> ERR %v", in, err)
		return err
	}
	if !s.inPool(addr) {
		s.log.Printf("%s -> ERR %v", in, ErrAddressNotInPool)
		return ErrAddressNotInPool
	}
	st := s.get(addr)
	s.sweep(st, now)
	if (st.leaseValid(now) && st.leaseClient != client) ||
		(st.reserveValid(now) && st.reserveClient != client) {
		s.log.Printf("%s -> ERR %v (lease by %q until %d, reservation by %q until %d)",
			in, ErrHeldByOther, st.leaseClient, st.leaseEnd, st.reserveClient, st.reserveEnd)
		return ErrHeldByOther
	}
	hasLease := st.leaseValid(now) && st.leaseClient == client
	hasReserve := st.reserveValid(now) && st.reserveClient == client
	if !hasLease && !hasReserve {
		s.log.Printf("%s -> ERR %v", in, ErrNoReservation)
		return ErrNoReservation
	}

	if hasReserve {
		s.clearReserve(st)
	}
	// A fresh lease starts at now; renewal does not stack remaining time.
	st.leaseClient = client
	st.leaseEnd = now + s.cfg.LeaseTime
	s.leaseOf[client] = addr
	s.lastHeld[client] = addr
	s.advance(now)
	note := ""
	if hasLease {
		note = ", renewed from now without stacking remaining time"
	}
	s.log.Printf("%s -> OK (lease [%d,%d), reservation cleared%s)", in, now, st.leaseEnd, note)
	return nil
}

// Release frees an address held by the client.
func (s *Server) Release(now int64, client string, addr int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	in := fmt.Sprintf("RELEASE in={now:%d client:%q addr:%d}", now, client, addr)
	if err := s.checkClock(now); err != nil {
		s.log.Printf("%s -> ERR %v", in, err)
		return err
	}
	if !s.inPool(addr) {
		s.log.Printf("%s -> ERR %v", in, ErrAddressNotInPool)
		return ErrAddressNotInPool
	}
	st := s.get(addr)
	if !st.leaseValid(now) || st.leaseClient != client {
		s.sweep(st, now)
		s.log.Printf("%s -> ERR %v (holder=%q, leaseEnd=%d)", in, ErrNotHolder, st.leaseClient, st.leaseEnd)
		return ErrNotHolder
	}
	s.clearLease(st, now)
	s.lastHeld[client] = addr
	s.advance(now)
	s.log.Printf("%s -> OK (lease released; termination time=%d)", in, now)
	return nil
}

// Reject revokes the holder's/reserver's claim and quarantines the address.
func (s *Server) Reject(now int64, client string, addr int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	in := fmt.Sprintf("REJECT in={now:%d client:%q addr:%d}", now, client, addr)
	if err := s.checkClock(now); err != nil {
		s.log.Printf("%s -> ERR %v", in, err)
		return err
	}
	if !s.inPool(addr) {
		s.log.Printf("%s -> ERR %v", in, ErrAddressNotInPool)
		return ErrAddressNotInPool
	}
	st := s.get(addr)
	holder := st.leaseValid(now) && st.leaseClient == client
	s.sweep(st, now)
	reserver := st.reserveValid(now) && st.reserveClient == client
	if !holder && !reserver {
		s.log.Printf("%s -> ERR %v (holder=%q, reserver=%q)",
			in, ErrNotHolder, st.leaseClient, st.reserveClient)
		return ErrNotHolder
	}

	if holder {
		// Holder rejection terminates the lease at the rejection time.
		s.clearLease(st, now)
		s.lastHeld[client] = addr
	}
	if reserver {
		// Reserver rejection clears the reservation but does not change the
		// lease termination time.
		s.clearReserve(st)
	}
	st.quarantineEnd = now + s.cfg.Quarantine
	s.advance(now)
	s.log.Printf("%s -> OK (claim cleared by %s; quarantined [%d,%d))",
		in, map[bool]string{true: "holder", false: "reserver"}[holder], now, st.quarantineEnd)
	return nil
}
