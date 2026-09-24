// Package wmgr 实现多窗口管理：Ingest/Purge/Late/GC、已清理窗口集合、互斥。
package wmgr

import (
	"errors"
	"sync"

	"ontology/win"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrBadID          = errors.New("wmgr: 非法 id（空）")
	ErrBadVal         = errors.New("wmgr: 非法 val（<=0）")
	ErrNoSuchWindow   = errors.New("wmgr: 窗口不存在（从未创建）")
	ErrTooManyWindows = errors.New("wmgr: 窗口数超 maxWindows")
)

// Mgr 管理全部窗口。所有写操作同持 mu，GC 与 Late 因此互斥（不变量 3）。
type Mgr struct {
	mu       sync.Mutex
	T        int64
	max      int64
	wins     map[string]*win.Win
	purged   map[string]struct{} // 已清理且未复活的窗口集合，GC 只遍历它
	triggers int64

	lastGCChecked int64 // 非导出：最近一次 GC 检查过的窗口数
}

// New 构造管理器，T 为触发阈值，max 为窗口数上限。
func New(T, max int64) *Mgr {
	return &Mgr{T: T, max: max, wins: map[string]*win.Win{}, purged: map[string]struct{}{}}
}

func checkArgs(id string, val int64) error {
	if id == "" {
		return ErrBadID
	}
	if val <= 0 {
		return ErrBadVal
	}
	return nil
}

// Ingest 累计一条事件；窗口不存在则隐式创建（受 max 限制）。
// 先校验后动状态：任何拒绝都不留痕（不变量 4）。
func (m *Mgr) Ingest(id string, val int64) error {
	if err := checkArgs(id, val); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wins[id]
	if !ok {
		if int64(len(m.wins)) >= m.max {
			return ErrTooManyWindows
		}
		w = win.New()
		m.wins[id] = w
	}
	if w.Add(val, m.T) {
		m.triggers++
	}
	return nil
}

// Purge 清理窗口：冻结 agg、保留 trg，加入待回收集合。
func (m *Mgr) Purge(id string) error {
	if id == "" {
		return ErrBadID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wins[id]
	if !ok {
		return ErrNoSuchWindow
	}
	if w.State() == win.Active {
		w.Purge()
		m.purged[id] = struct{}{}
	}
	return nil
}

// Late 处理迟到事件：active 同 Ingest；purged 复活（只取这条 val）；
// revived 照常累加但抑制触发。从未创建的窗口报错且不留痕。
func (m *Mgr) Late(id string, val int64) error {
	if err := checkArgs(id, val); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wins[id]
	if !ok {
		return ErrNoSuchWindow
	}
	if w.State() == win.Purged {
		w.Revive(val)
		delete(m.purged, id)
		return nil
	}
	if w.Add(val, m.T) {
		m.triggers++
	}
	return nil
}

// GC 删除「已清理且未复活」的窗口；revived 保留。只遍历 purged 集合，
// 检查数与总窗口数无关。
func (m *Mgr) GC() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastGCChecked = 0
	for id := range m.purged {
		m.lastGCChecked++
		if w, ok := m.wins[id]; ok && w.State() == win.Purged {
			delete(m.wins, id)
			delete(m.purged, id)
		}
	}
}

// Snapshot 读取窗口状态；ok=false 表示窗口不存在。
func (m *Mgr) Snapshot(id string) (agg, trg int64, state win.State, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wins[id]
	if !ok {
		return 0, 0, win.Active, false
	}
	return w.Agg(), w.Trg(), w.State(), true
}

// Triggers 返回累计触发器事件数。
func (m *Mgr) Triggers() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.triggers
}
