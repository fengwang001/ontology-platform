// Package pit 实现时间点视图（PIT）的开启、续期、关闭与过期落地。
// Manager 不持有自己的锁：其全部方法都在 segstore.Store 的锁内执行
// （公开方法经 Store.RunOp 进入，回调由 Store 在锁内发起）。
package pit

import (
	"errors"

	"ontology/segstore"
)

var (
	// ErrNotFound PIT 不存在（含已过期被落地）。
	ErrNotFound = errors.New("pit: pit not found")
	// ErrLimit 落地过期之后仍存活的 PIT 数已达 Pmax。
	ErrLimit = errors.New("pit: pit limit exceeded")
)

// MaxKA 是 Open/Search 中 ka 的上界（毫秒）。
const MaxKA = int64(1_000_000_000)

// PIT 是一个时间点视图：开启时刻的段号集合 + 开启操作号。
// 段内一篇文档在该 PIT 中可见，当且仅当段号在 Segs 内，
// 且未被删除或删除操作号大于 OpenOp。
type PIT struct {
	ID     int64
	Segs   []int64 // 开启时刻当前视图的段号（升序）
	OpenOp int64   // 开启操作占用的全局操作号
	Exp    int64   // 过期时刻，now >= Exp 即过期
}

// Manager 管理全部存活 PIT，并实现 segstore.Pinner 供 Store 回调。
type Manager struct {
	store   *segstore.Store
	pmax    int
	seq     int64          // PIT 编号，自 1 起
	pits    map[int64]*PIT // 未落地的 PIT
	pins    map[int64]int  // 段号 -> 持有它的 PIT 数
	touched int64          // 非导出计数器：Open/Close 触碰的段记录数
}

// NewManager 创建 PIT 管理器并把它注册到 store。pmax 须在 [1, 1000]。
func NewManager(store *segstore.Store, pmax int) *Manager {
	if pmax < 1 || pmax > 1000 {
		panic("pit: Pmax out of range [1, 1000]")
	}
	m := &Manager{
		store: store,
		pmax:  pmax,
		pits:  make(map[int64]*PIT),
		pins:  make(map[int64]int),
	}
	store.SetPinner(m)
	return m
}

// LandExpired 落地全部 exp <= now 的 PIT，返回它们释放引用的段号。
// 只由 Store 在锁内调用。
func (m *Manager) LandExpired(now int64) (dropped []int64) {
	for pid, p := range m.pits {
		if p.Exp <= now {
			dropped = append(dropped, p.Segs...)
			m.unpin(p)
			delete(m.pits, pid)
		}
	}
	return dropped
}

// Holds 报告段 seg 是否仍被某个未落地 PIT 持有。只由 Store 在锁内调用。
func (m *Manager) Holds(seg int64) bool { return m.pins[seg] > 0 }

// pin/unpin 只触碰段号记录，与文档总数无关；touched 用于证明这一点。
func (m *Manager) pin(segs []int64) {
	for _, n := range segs {
		m.pins[n]++
		m.touched++
	}
}

func (m *Manager) unpin(p *PIT) {
	for _, n := range p.Segs {
		m.pins[n]--
		if m.pins[n] == 0 {
			delete(m.pins, n)
		}
		m.touched++
	}
}

// Open 以当前视图的段号集合开启一个 PIT，exp = now + ka，返回 PIT 编号。
func (m *Manager) Open(now, ka int64) (int64, error) {
	if ka < 1 || ka > MaxKA || now < 0 || now > segstore.MaxNow {
		return 0, segstore.ErrInvalidParam
	}
	var pid int64
	err := m.store.RunOp(now, func(tx *segstore.Tx) error {
		if len(m.pits) >= m.pmax {
			return ErrLimit
		}
		view := tx.View()
		op := tx.NextOp()
		m.seq++
		pid = m.seq
		m.pits[pid] = &PIT{ID: pid, Segs: view, OpenOp: op, Exp: now + ka}
		m.pin(view)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// Close 立即落地一个 PIT。
func (m *Manager) Close(now, pid int64) error {
	if now < 0 || now > segstore.MaxNow {
		return segstore.ErrInvalidParam
	}
	return m.store.RunOp(now, func(tx *segstore.Tx) error {
		p, ok := m.pits[pid]
		if !ok {
			return ErrNotFound
		}
		m.unpin(p)
		delete(m.pits, pid)
		tx.ReleaseCandidates(p.Segs...)
		return nil
	})
}

// Lookup 查找未落地的 PIT。仅供 search 包在 Store.RunOp 内调用。
func (m *Manager) Lookup(pid int64) (*PIT, bool) {
	p, ok := m.pits[pid]
	return p, ok
}

// Renew 把 PIT 的过期时刻延长到 exp（只延不缩）。
// 仅供 search 包在 Store.RunOp 内调用。
func (m *Manager) Renew(pid int64, exp int64) {
	if p, ok := m.pits[pid]; ok && exp > p.Exp {
		p.Exp = exp
	}
}
