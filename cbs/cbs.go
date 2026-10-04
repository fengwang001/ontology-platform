// Package cbs implements a bandwidth ledger for Constant Bandwidth Servers
// (CBS) with a zero-slack reclaimer.
//
// Each server has a budget Q and period P (1 <= Q <= P <= 1e6) and reserves
// the bandwidth Q/P. A server occupies its bandwidth while it is ready
// (pending work w > 0) or idle-holding (w == 0 but its zero-slack instant
// has not been reached yet). A released server occupies nothing.
//
// Zero-slack rule (exact, 128-bit-safe): a woken server with w == 0 is
// released iff
//
//	q * P >= (d - now) * Q
//
// where q is the remaining budget and d the current deadline. The products
// can reach 1e21 and beyond, so all comparisons use big.Int.
//
// All methods are safe for concurrent use; the result is equivalent to some
// serial order (a single mutex serializes every operation and query).
package cbs

import (
	"fmt"
	"math/big"
	"sync"
)

// Limits mandated by the specification.
const (
	MaxServers = 32
	MaxQP      = 1_000_000
	MaxWork    = 1_000_000_000
	MaxBacklog = 1_000_000_000_000_000 // 1e15, upper bound for w and now
)

// ErrKind identifies the rejection reason of an operation. The numeric
// order matches the mandated reporting priority: only the first applicable
// reason is reported.
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota // empty id or out-of-range numeric value
	ErrDuplicate                   // Add: id already exists
	ErrCapacityFull                // Add: already MaxServers servers
	ErrNotFound                    // id does not exist
	ErrClockRewind                 // now is smaller than the current clock
	ErrBandwidth                   // Wake: not enough free bandwidth to admit
	ErrNotRunnable                 // Run: server not ready or delta > w
	ErrNotEarliest                 // Run: server is not the earliest-deadline ready one
	ErrBusy                        // Remove: server is not released
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrDuplicate:
		return "编号重复"
	case ErrCapacityFull:
		return "容量已满"
	case ErrNotFound:
		return "编号不存在"
	case ErrClockRewind:
		return "时钟回退"
	case ErrBandwidth:
		return "带宽不足"
	case ErrNotRunnable:
		return "不可运行"
	case ErrNotEarliest:
		return "非最早截止"
	case ErrBusy:
		return "占用中"
	}
	return "未知错误"
}

// Error is the single error type returned by every mutating operation.
type Error struct {
	Op   string
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

func newErr(op string, kind ErrKind, format string, args ...any) *Error {
	return &Error{Op: op, Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// State is the lifecycle state of a server, a pure function of the clock.
type State int

const (
	// Ready: w > 0, occupies bandwidth.
	Ready State = iota
	// Idle: w == 0, woken before, zero-slack instant not reached; occupies bandwidth.
	Idle
	// Released: never woken, or zero-slack instant reached; occupies nothing.
	Released
)

func (s State) String() string {
	switch s {
	case Ready:
		return "就绪"
	case Idle:
		return "空闲占用"
	case Released:
		return "已释放"
	}
	return "未知状态"
}

// View is a snapshot of one server returned by State.
type View struct {
	ID       string
	State    State
	Q, P     int64
	Budget   int64    // remaining budget q
	Deadline *big.Int // current deadline d
	Work     int64    // pending work w
}

type server struct {
	id    string
	Q, P  int64
	q     int64
	d     *big.Int // may exceed int64 (up to ~1e21) after many replenishments
	w     int64
	woken bool
}

// stateAt classifies s at the given clock value. Pure function of now.
func stateAt(s *server, now int64) State {
	if s.w > 0 {
		return Ready
	}
	if !s.woken {
		return Released
	}
	// Released iff q*P >= (d-now)*Q, computed in big.Int (products may
	// exceed 64 bits).
	lhs := new(big.Int).Mul(big.NewInt(s.q), big.NewInt(s.P))
	rhs := new(big.Int).Sub(s.d, big.NewInt(now))
	rhs.Mul(rhs, big.NewInt(s.Q))
	if lhs.Cmp(rhs) >= 0 {
		return Released
	}
	return Idle
}

// occupies reports whether s reserves bandwidth at now.
func occupies(s *server, now int64) bool {
	return stateAt(s, now) != Released
}

// bandwidth returns Q/P as an exact rational.
func (s *server) bandwidth() *big.Rat {
	return new(big.Rat).SetFrac64(s.Q, s.P)
}

// Ledger is the bandwidth bookkeeper for up to MaxServers CBS servers.
// The zero value is ready to use.
type Ledger struct {
	mu      sync.Mutex
	clock   int64
	servers map[string]*server
}

// NewLedger returns an empty ledger with clock 0.
func NewLedger() *Ledger {
	return &Ledger{servers: make(map[string]*server)}
}

// Clock returns the current system clock.
func (l *Ledger) Clock() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clock
}

// Add registers a new server. Only ErrInvalidParam, ErrDuplicate and
// ErrCapacityFull are possible.
func (l *Ledger) Add(id string, Q, P int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" || Q < 1 || P < Q || P > MaxQP {
		return newErr("Add", ErrInvalidParam, "id=%q Q=%d P=%d（要求非空编号且 1≤Q≤P≤%d）", id, Q, P, MaxQP)
	}
	if l.servers == nil {
		l.servers = make(map[string]*server)
	}
	if _, ok := l.servers[id]; ok {
		return newErr("Add", ErrDuplicate, "id=%q 已存在", id)
	}
	if len(l.servers) >= MaxServers {
		return newErr("Add", ErrCapacityFull, "已有 %d 个服务器", len(l.servers))
	}
	l.servers[id] = &server{id: id, Q: Q, P: P, q: Q, d: new(big.Int)}
	return nil
}

// totalOccupiedLocked sums the bandwidth of all servers occupying at now.
func (l *Ledger) totalOccupiedLocked(now int64) *big.Rat {
	total := new(big.Rat)
	for _, s := range l.servers {
		if occupies(s, now) {
			total.Add(total, s.bandwidth())
		}
	}
	return total
}

// Wake delivers work units to a server.
//
//   - ready server: only accumulates w.
//   - idle-holding server: keeps q and d, sets w = work.
//   - released server: admitted only if the bandwidth occupied by the other
//     servers plus Q/P does not exceed 1; then q=Q, d=now+P, w=work.
//     Otherwise rejected with ErrBandwidth and nothing changes.
func (l *Ledger) Wake(id string, now, work int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" || work < 1 || work > MaxWork || now < 0 || now > MaxBacklog {
		return newErr("Wake", ErrInvalidParam, "id=%q now=%d work=%d（要求非空编号、0≤now≤%d、1≤work≤%d）",
			id, now, work, MaxBacklog, MaxWork)
	}
	s, ok := l.servers[id]
	if !ok {
		return newErr("Wake", ErrNotFound, "id=%q", id)
	}
	if now < l.clock {
		return newErr("Wake", ErrClockRewind, "now=%d < clock=%d", now, l.clock)
	}
	switch stateAt(s, now) {
	case Ready:
		if s.w+work > MaxBacklog {
			return newErr("Wake", ErrInvalidParam, "累加后 w=%d 超过 %d", s.w+work, MaxBacklog)
		}
		s.w += work
	case Idle:
		s.w = work
	case Released:
		total := l.totalOccupiedLocked(now)
		total.Add(total, s.bandwidth())
		if total.Cmp(big.NewRat(1, 1)) > 0 {
			return newErr("Wake", ErrBandwidth, "id=%q 接纳后总带宽 %s 超过 1", id, total.RatString())
		}
		s.q = s.Q
		s.d.SetInt64(now + s.P)
		s.w = work
	}
	s.woken = true
	l.clock = now
	return nil
}

// earliestReadyLocked returns the ready server with the smallest deadline,
// ties broken by bytewise id order. nil if none is ready.
func (l *Ledger) earliestReadyLocked() *server {
	var best *server
	for _, s := range l.servers {
		if s.w == 0 {
			continue
		}
		if best == nil || s.d.Cmp(best.d) < 0 ||
			(s.d.Cmp(best.d) == 0 && s.id < best.id) {
			best = s
		}
	}
	return best
}

// Run lets the server run continuously for delta units starting at now.
// The server must be ready, delta <= w, and it must be the ready server
// with the earliest deadline (ties: bytewise smallest id). Each consumed
// unit decrements q; when q reaches 0 it is immediately refilled to Q and
// d advances by P (possibly several times within one Run).
func (l *Ledger) Run(id string, now, delta int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" || delta < 1 || now < 0 || now > MaxBacklog {
		return newErr("Run", ErrInvalidParam, "id=%q now=%d delta=%d（要求非空编号、0≤now≤%d、δ≥1）",
			id, now, delta, MaxBacklog)
	}
	s, ok := l.servers[id]
	if !ok {
		return newErr("Run", ErrNotFound, "id=%q", id)
	}
	if now < l.clock {
		return newErr("Run", ErrClockRewind, "now=%d < clock=%d", now, l.clock)
	}
	if s.w == 0 || delta > s.w {
		return newErr("Run", ErrNotRunnable, "id=%q w=%d delta=%d", id, s.w, delta)
	}
	if earliest := l.earliestReadyLocked(); earliest != s {
		return newErr("Run", ErrNotEarliest, "id=%q 的就绪截止期不早于 id=%q", id, earliest.id)
	}
	// Bulk-consume delta units with immediate replenishment, equivalent to
	// the per-unit loop: q--; if q == 0 { q = Q; d += P }.
	if delta < s.q {
		s.q -= delta
	} else {
		k := (delta-s.q)/s.Q + 1 // number of replenishments
		s.q = s.q - delta + k*s.Q
		s.d.Add(s.d, new(big.Int).Mul(big.NewInt(k), big.NewInt(s.P)))
	}
	s.w -= delta
	l.clock = now + delta
	return nil
}

// Remove deletes a released server. Any other state reports ErrBusy.
func (l *Ledger) Remove(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		return newErr("Remove", ErrInvalidParam, "编号为空")
	}
	s, ok := l.servers[id]
	if !ok {
		return newErr("Remove", ErrNotFound, "id=%q", id)
	}
	if st := stateAt(s, l.clock); st != Released {
		return newErr("Remove", ErrBusy, "id=%q 状态为 %s", id, st)
	}
	delete(l.servers, id)
	return nil
}

// State returns a snapshot of one server, classified at the current clock.
func (l *Ledger) State(id string) (View, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		return View{}, newErr("State", ErrInvalidParam, "编号为空")
	}
	s, ok := l.servers[id]
	if !ok {
		return View{}, newErr("State", ErrNotFound, "id=%q", id)
	}
	return View{
		ID:       s.id,
		State:    stateAt(s, l.clock),
		Q:        s.Q,
		P:        s.P,
		Budget:   s.q,
		Deadline: new(big.Int).Set(s.d),
		Work:     s.w,
	}, nil
}

// Total returns the exact reduced fraction of the bandwidth occupied at
// the current clock. It never exceeds 1.
func (l *Ledger) Total() *big.Rat {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.totalOccupiedLocked(l.clock)
}

// Next returns the id of the ready server with the earliest deadline
// (ties: bytewise smallest id), or false if no server is ready.
func (l *Ledger) Next() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.earliestReadyLocked(); s != nil {
		return s.id, true
	}
	return "", false
}
