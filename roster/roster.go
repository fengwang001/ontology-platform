package roster

import (
	"errors"
	"sync"
)

const (
	maxTime   = 1_000_000_000_000
	maxSnSize = 64
	maxBatchN = 10_000
)

type (
	Sn     string
	Batch  string
	Tenant string
	Fp     string
)

type State int

const (
	Registered State = iota
	Activated
	ResetPending
)

var (
	ErrInvalid     = errors.New("roster: invalid argument")
	ErrDupSn       = errors.New("roster: duplicate sn")
	ErrUnknown     = errors.New("roster: unknown sn")
	ErrClockBack   = errors.New("roster: clock moved backwards")
	ErrState       = errors.New("roster: invalid state for operation")
	ErrBatchClosed = errors.New("roster: batch already closed")
	ErrLocked      = errors.New("roster: sn is locked")
	ErrConflict    = errors.New("roster: fingerprint conflict")
	ErrQuota       = errors.New("roster: tenant quota exhausted")
)

type Record struct {
	Batch  Batch
	Tenant Tenant
	Until  int64
	State  State
	ID     int64
	Gen    int64
	Fp     Fp
}

type DupError struct {
	Index int
}

func (DupError) Error() string { return ErrDupSn.Error() }
func (DupError) Unwrap() error { return ErrDupSn }

func DupIndex(err error) (int, bool) {
	var d DupError
	if errors.As(err, &d) {
		return d.Index, true
	}
	return -1, false
}

type record struct {
	batch  Batch
	tenant Tenant
	until  int64
	state  State
	id     int64
	gen    int64
	fp     Fp
}

func (x *record) snap() Record {
	return Record{
		Batch:  x.batch,
		Tenant: x.tenant,
		Until:  x.until,
		State:  x.state,
		ID:     x.id,
		Gen:    x.gen,
		Fp:     x.fp,
	}
}

func validTime(t int64) bool { return t >= 0 && t <= maxTime }

func validBytes(s string) bool {
	n := len(s)
	return n >= 1 && n <= maxSnSize
}

type Roster struct {
	mu     sync.Mutex
	recs   map[Sn]*record
	nextID int64
	now    int64
	probes int64
}

func New() *Roster {
	return &Roster{recs: make(map[Sn]*record)}
}

// RegisterBatch 全有或全无：纯读阶段查重，确认无冲突后统一写入。
func (r *Roster) RegisterBatch(batch Batch, tenant Tenant, until int64, sns []Sn, now int64) error {
	if batch == "" || tenant == "" || !validTime(until) || !validTime(now) ||
		len(sns) < 1 || len(sns) > maxBatchN {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return ErrClockBack
	}
	for _, sn := range sns {
		if !validBytes(string(sn)) {
			return ErrInvalid
		}
	}
	dup := -1
	seen := make(map[Sn]struct{}, len(sns))
	for i, sn := range sns {
		r.probes++
		if _, ok := seen[sn]; ok {
			if dup == -1 || i < dup {
				dup = i
			}
		} else {
			seen[sn] = struct{}{}
		}
		r.probes++
		if _, ok := r.recs[sn]; ok {
			if dup == -1 || i < dup {
				dup = i
			}
		}
	}
	if dup >= 0 {
		return DupError{Index: dup}
	}
	for _, sn := range sns {
		r.recs[sn] = &record{
			batch:  batch,
			tenant: tenant,
			until:  until,
			state:  Registered,
		}
	}
	r.now = now
	return nil
}

// Hooks 在 roster 临界区内被回调，用于访问 guard 与租户名额。
// 加锁顺序固定为 roster.mu -> 实现方内部锁；实现方不得反向进入 roster。
type Hooks interface {
	Locked(sn Sn, now int64) bool
	NoteConflict(sn Sn, now int64)
	AcquireQuota(tenant Tenant) bool
	ClearGuard(sn Sn)
	ReleaseQuota(tenant Tenant)
}

// Gate 是 Activate 的全部判定与状态迁移，按拒绝次序只返回第一个错误。
// ErrConflict 是唯一改状态的拒绝（推进时钟并记录错误尝试）。
func (r *Roster) Gate(sn Sn, fp Fp, now int64, h Hooks) (Record, error) {
	if !validBytes(string(sn)) || !validBytes(string(fp)) || !validTime(now) {
		return Record{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return Record{}, ErrClockBack
	}
	r.probes++
	x, ok := r.recs[sn]
	if !ok {
		return Record{}, ErrUnknown
	}
	if h.Locked(sn, now) {
		return Record{}, ErrLocked
	}
	switch x.state {
	case Activated:
		if fp == x.fp {
			r.now = now
			return x.snap(), nil
		}
		r.now = now
		h.NoteConflict(sn, now)
		return x.snap(), ErrConflict
	case Registered:
		if now >= x.until {
			return Record{}, ErrBatchClosed
		}
		if !h.AcquireQuota(x.tenant) {
			return Record{}, ErrQuota
		}
		r.now = now
		x.fp = fp
		x.state = Activated
		if x.id == 0 {
			r.nextID++
			x.id = r.nextID
		}
		x.gen++
		return x.snap(), nil
	case ResetPending:
		r.now = now
		x.fp = fp
		x.state = Activated
		x.gen++
		return x.snap(), nil
	default:
		return Record{}, ErrState
	}
}

// Reset 仅对 Activated 有效；清指纹、错误计数与锁定，k 由 guard 保留。
func (r *Roster) Reset(sn Sn, now int64, h Hooks) (Record, error) {
	if !validBytes(string(sn)) || !validTime(now) {
		return Record{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return Record{}, ErrClockBack
	}
	r.probes++
	x, ok := r.recs[sn]
	if !ok {
		return Record{}, ErrUnknown
	}
	if x.state != Activated {
		return Record{}, ErrState
	}
	r.now = now
	x.state = ResetPending
	x.fp = ""
	h.ClearGuard(sn)
	return x.snap(), nil
}

// Deactivate 对 Activated/ResetPending 有效；保留 id/gen，释放名额。
func (r *Roster) Deactivate(sn Sn, now int64, h Hooks) (Record, error) {
	if !validBytes(string(sn)) || !validTime(now) {
		return Record{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.now {
		return Record{}, ErrClockBack
	}
	r.probes++
	x, ok := r.recs[sn]
	if !ok {
		return Record{}, ErrUnknown
	}
	if x.state != Activated && x.state != ResetPending {
		return Record{}, ErrState
	}
	r.now = now
	x.state = Registered
	x.fp = ""
	h.ReleaseQuota(x.tenant)
	return x.snap(), nil
}

// Snapshot 返回 sn 当前名单记录的只读副本。
func (r *Roster) Snapshot(sn Sn) (Record, error) {
	if !validBytes(string(sn)) {
		return Record{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes++
	x, ok := r.recs[sn]
	if !ok {
		return Record{}, ErrUnknown
	}
	return x.snap(), nil
}

// Probes 返回名单 map 累计探查次数（非导出计数器的只读出口）。
func (r *Roster) Probes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probes
}
