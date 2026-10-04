package roster

import "sync"

// 公共哨兵错误：activate 包复用这些错误。
var (
	ErrInvalid       = errInvalid{}
	ErrClockBack     = errClockBack{}
	ErrUnknown       = errUnknown{}
	ErrUnknownTenant = errUnknownTenant{}
	ErrDupSn         = &DupSnError{}
)

type errInvalid struct{}

func (errInvalid) Error() string { return "invalid argument" }

type errClockBack struct{}

func (errClockBack) Error() string { return "clock moved backwards" }

type errUnknown struct{}

func (errUnknown) Error() string { return "sn not registered" }

type errUnknownTenant struct{}

func (errUnknownTenant) Error() string { return "unknown tenant" }

// DupSnError 携带整批拒绝时下标最小的重复项（批内重复取后一次出现的下标）。
type DupSnError struct{ Idx int }

func (e *DupSnError) Error() string { return "duplicate sn in batch" }

// Is 使任意带下标的 DupSnError 都可与哨兵 ErrDupSn 比较。
func (e *DupSnError) Is(target error) bool {
	_, ok := target.(*DupSnError)
	return ok
}

// State 是单个序列号的生命周期状态。
type State int

const (
	Registered State = iota
	Activated
	ResetPending
)

// Record 是某 sn 当前状态的只读快照。
type Record struct {
	State  State
	ID     int64
	Gen    int64
	FP     []byte
	Until  int64
	Tenant string
}

// Roster 持有批次名单、租户名额、全局 id 计数与全局单调时钟。
type Roster struct {
	mu      sync.Mutex
	maxNow  int64
	nextID  int64
	probes  int64
	records map[string]*record
	tenants map[string]*tenant
}

type record struct {
	state  State
	id     int64
	gen    int64
	fp     []byte
	until  int64
	tenant string
}

type tenant struct {
	quota int
	used  int
}

// New 创建空名单。
func New() *Roster {
	return &Roster{
		records: make(map[string]*record),
		tenants: make(map[string]*tenant),
	}
}

func validBytes(b []byte) bool { return len(b) >= 1 && len(b) <= 64 }

func validTime(t int64) bool { return 0 <= t && t <= 1_000_000_000_000 }

// Lock / Unlock 供 activate 编排层在名单锁内同时操作 guard，保证全局串行化。
func (r *Roster) Lock()   { r.mu.Lock() }
func (r *Roster) Unlock() { r.mu.Unlock() }

// AdvanceClockLocked 校验时钟单调性并推进 maxNow；调用方须持锁。
func (r *Roster) AdvanceClockLocked(now int64) error {
	if now < r.maxNow {
		return ErrClockBack
	}
	r.maxNow = now
	return nil
}

// BeginOpLocked 开始一次探查并返回名单记录（不存在返回 nil）；调用方须持锁。
func (r *Roster) BeginOpLocked(sn []byte) *Record {
	r.probes = 0
	rec := r.records[string(sn)]
	r.probes++
	if rec == nil {
		return nil
	}
	return rec.snapshot()
}

// QuotaLocked 返回某租户 (used, quota, ok)；调用方须持锁，记一次名额探查。
func (r *Roster) QuotaLocked(name string) (used, quota int, ok bool) {
	t := r.tenants[name]
	r.probes++
	if t == nil {
		return 0, 0, false
	}
	return t.used, t.quota, true
}

// ProbesLocked 返回本次探查计数；调用方须持锁。
func (r *Roster) ProbesLocked() int { return int(r.probes) }

// Probes 返回最近一次操作的探查数（供测试对照，内部加锁）。
func (r *Roster) Probes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int(r.probes)
}

// AllocIDLocked 分配下一个全局连续 id；调用方须持锁。
func (r *Roster) AllocIDLocked() int64 {
	r.nextID++
	return r.nextID
}

// SetFirstBindLocked 将 Registered 记录置为首次激活；调用方须持锁。
func (r *Roster) SetFirstBindLocked(sn, fp []byte) (id, gen int64) {
	rec := r.records[string(sn)]
	if rec.id == 0 {
		rec.id = r.nextID + 1
		r.nextID = rec.id
	}
	rec.gen++
	rec.fp = append([]byte(nil), fp...)
	rec.state = Activated
	r.tenants[rec.tenant].used++
	return rec.id, rec.gen
}

// SetRebindLocked 将 Activated（幂等重放）或 ResetPending（重绑）记录更新；调用方须持锁。
func (r *Roster) SetRebindLocked(sn, fp []byte) (id, gen int64, replayed bool) {
	rec := r.records[string(sn)]
	id = rec.id
	gen = rec.gen
	if rec.state == ResetPending {
		rec.gen++
		gen = rec.gen
		rec.fp = append([]byte(nil), fp...)
		rec.state = Activated
		return id, gen, false
	}
	return id, gen, true
}

// SetResetLocked 将 Activated 记录置为 ResetPending 并清指纹；调用方须持锁。
func (r *Roster) SetResetLocked(sn []byte) {
	rec := r.records[string(sn)]
	rec.state = ResetPending
	rec.fp = nil
}

// SetDeactivatedLocked 释放名额、清指纹、状态回 Registered；调用方须持锁。
func (r *Roster) SetDeactivatedLocked(sn []byte) {
	rec := r.records[string(sn)]
	rec.state = Registered
	rec.fp = nil
	r.tenants[rec.tenant].used--
}

// GetLocked 返回记录快照或 nil；调用方须持锁。
func (r *Roster) GetLocked(sn []byte) *Record {
	rec := r.records[string(sn)]
	if rec == nil {
		return nil
	}
	return rec.snapshot()
}

// Get 返回记录快照或 nil（内部加锁，供测试与外部观察）。
func (r *Roster) Get(sn []byte) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.GetLocked(sn)
}

// MaxNowLocked 返回已接受的最大 now；调用方须持锁。
func (r *Roster) MaxNowLocked() int64 { return r.maxNow }

func (rec *record) snapshot() *Record {
	out := &Record{
		State:  rec.state,
		ID:     rec.id,
		Gen:    rec.gen,
		Until:  rec.until,
		Tenant: rec.tenant,
	}
	if rec.fp != nil {
		out.FP = append([]byte(nil), rec.fp...)
	}
	return out
}

// AddTenant 建立租户及其名额。
func (r *Roster) AddTenant(name string, n int) error {
	if name == "" || n < 1 || n > 1_000_000 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tenants[name]; exists {
		return ErrInvalid
	}
	r.tenants[name] = &tenant{quota: n}
	return nil
}

// RegisterBatch 预登记一批序列号。
func (r *Roster) RegisterBatch(batch, tenant string, until int64, sns [][]byte, now int64) error {
	if batch == "" || !validTime(until) || !validTime(now) {
		return ErrInvalid
	}
	if len(sns) < 1 || len(sns) > 10_000 {
		return ErrInvalid
	}
	for _, sn := range sns {
		if !validBytes(sn) {
			return ErrInvalid
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = 0
	_, exists := r.tenants[tenant]
	if !exists {
		return ErrUnknownTenant
	}
	if now < r.maxNow {
		return ErrClockBack
	}
	// 预扫描：找下标最小的重复项（批内重复以后一次出现的下标计），命中即拒绝，零写入。
	dup := -1
	seen := make(map[string]int, len(sns))
	for i, sn := range sns {
		key := string(sn)
		if _, ok := seen[key]; ok {
			if dup == -1 || i < dup {
				dup = i
			}
		}
		if _, ok := r.records[key]; ok {
			if dup == -1 || i < dup {
				dup = i
			}
		}
		r.probes++ // 预扫描读取：每 sn 1 次
		seen[key] = i
	}
	if dup != -1 {
		return &DupSnError{Idx: dup}
	}
	for _, sn := range sns {
		r.records[string(sn)] = &record{state: Registered, until: until, tenant: tenant}
		r.probes++ // 接受时写入：每 sn 1 次，与预扫描合计 ≤2×批长
	}
	r.maxNow = now
	return nil
}
