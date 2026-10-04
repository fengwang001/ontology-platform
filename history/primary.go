package history

import (
	"sync"

	"ontology/lease"
	"ontology/recovery"
)

const maxNow = 1_000_000_000_000

func validNow(now int) bool { return now >= 0 && now <= maxNow }

func validID(id string) bool { return len(id) >= 1 && len(id) <= 256 }

// Primary 是主分片协调器：历史 + 租约管理器 + 单调时钟。
// 单互斥锁串行化全部操作，并发结果等价于某个串行顺序。
type Primary struct {
	mu      sync.Mutex
	hist    *History
	leases  *lease.Manager
	now     int
	nowSeen bool
}

// NewPrimary 创建协调器。E 为租约有效期（秒），Lmax 为租约名额上限。
func NewPrimary(E, lmax int) *Primary {
	if E < 1 || E > 1_000_000_000 || lmax < 1 || lmax > 1000 {
		panic("history: invalid lease parameters")
	}
	h := New()
	return &Primary{
		hist:   h,
		leases: lease.New(E, lmax, h),
	}
}

// MaxSeq / H / GCP 为只读快照。
func (p *Primary) MaxSeq() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hist.MaxSeq()
}

func (p *Primary) H() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hist.H()
}

func (p *Primary) GCP() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leases.GCP()
}

func (p *Primary) LeaseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leases.Count()
}

// Leases 返回现存租约快照（名字升序），含过期但尚未被剔除者。
func (p *Primary) Leases() []lease.LeaseView {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leases.Snapshot()
}

// checkNow 强制 now 范围与单调时钟；失败返回对应错误且不推进任何状态。
func (p *Primary) checkNow(now int) error {
	if !validNow(now) {
		return ErrInvalidArgument
	}
	if p.nowSeen && now < p.now {
		return ErrClockBacktrack
	}
	return nil
}

func (p *Primary) advance(now int) {
	p.now = now
	p.nowSeen = true
}

func (p *Primary) Index(now int, id string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validID(id) {
		return 0, ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return 0, err
	}
	p.advance(now)
	return p.hist.Append(id, KindIndex), nil
}

func (p *Primary) Delete(now int, id string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validID(id) {
		return 0, ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return 0, err
	}
	if !p.hist.Live(id) {
		return 0, ErrDocNotFound
	}
	p.advance(now)
	return p.hist.Append(id, KindDelete), nil
}

// AddLease 拒绝次序：参数非法 > 时钟回退 > 已存在 > 历史不可得(r<H) > 超限。
func (p *Primary) AddLease(now int, name string, r int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validNow(now) || name == "" || r < 1 || r > p.hist.MaxSeq()+1 {
		return lease.ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	if err := p.leases.AddLease(now, name, r); err != nil {
		return err
	}
	p.advance(now)
	return nil
}

// RenewLease 拒绝次序：参数非法 > 时钟回退 > 不存在 > 租约回退。
func (p *Primary) RenewLease(now int, name string, r int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validNow(now) || name == "" {
		return lease.ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	if err := p.leases.RenewLease(now, name, r); err != nil {
		return err
	}
	p.advance(now)
	return nil
}

func (p *Primary) RemoveLease(now int, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validNow(now) || name == "" {
		return lease.ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	if err := p.leases.RemoveLease(name); err != nil {
		return err
	}
	p.advance(now)
	return nil
}

// SetGlobalCheckpoint 拒绝次序：参数非法(g>maxSeq) > 时钟回退 > 检查点回退。
func (p *Primary) SetGlobalCheckpoint(now, g int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validNow(now) || g < 0 || g > p.hist.MaxSeq() {
		return lease.ErrInvalidArgument
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	if err := p.leases.SetGCP(g); err != nil {
		return err
	}
	p.advance(now)
	return nil
}

// Merge：先剔除全部过期租约，再按 floor 清除并推进 H。被拒不改变状态。
func (p *Primary) Merge(now int) (MergeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNow(now); err != nil {
		return MergeResult{}, err
	}
	floor, removed := p.leases.MergeFloor(now)
	purged := p.hist.Purge(floor)
	p.advance(now)
	return MergeResult{Purged: purged, Removed: removed}, nil
}

func (p *Primary) Plan(c int) (recovery.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return recovery.PlanRecovery(lockedSource{p: p}, c)
}

// lockedSource 是持锁期间访问 Primary 的免锁 Source 适配器。
type lockedSource struct{ p *Primary }

func (s lockedSource) MaxSeq() int { return s.p.hist.MaxSeq() }
func (s lockedSource) H() int      { return s.p.hist.H() }

func (s lockedSource) OpsAfter(c int) []recovery.Op {
	p := s.p
	ops := p.hist.OpsAfter(c)
	out := make([]recovery.Op, len(ops))
	for i, op := range ops {
		out[i] = recovery.Op{Seq: op.Seq, ID: op.ID, Kind: recovery.Kind(op.Kind)}
	}
	return out
}

func (s lockedSource) LiveDocs() []recovery.Doc {
	p := s.p
	docs := p.hist.LiveDocs()
	out := make([]recovery.Doc, len(docs))
	for i, d := range docs {
		out[i] = recovery.Doc{ID: d.ID, Seq: d.Seq}
	}
	return out
}
