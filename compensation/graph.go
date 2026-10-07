package compensation

import (
	"sort"
	"sync"
)

// Graph 是被副作用修改的"对象图"。它以 key -> 整数值的形式建模
// 对象图中可被原子修改的属性单元（例如边的基数、对象属性计数）。
//
// 所有读写都由调用方持有的锁保护：动作级通过 LockManager 获取
// 键级锁后再操作 Graph；Graph 自身的互斥只保证 map 结构安全。
type Graph struct {
	mu sync.Mutex
	k  map[string]int64
}

// NewGraph 创建空对象图。
func NewGraph() *Graph {
	return &Graph{k: make(map[string]int64)}
}

// Get 返回键的当前值（不存在即为 0）。
func (g *Graph) Get(key string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.k[key]
}

// Add 给键叠加一个可正可负的增量（交换律原语）。
func (g *Graph) Add(key string, delta int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.k[key] += delta
}

// Snapshot 返回 key -> value 的深拷贝，用于测试对拍与终态断言。
func (g *Graph) Snapshot() map[string]int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]int64, len(g.k))
	for k, v := range g.k {
		out[k] = v
	}
	return out
}

// IsClean 报告对象图是否所有已知键的值都为 0（补偿后应满足）。
func (g *Graph) IsClean() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, v := range g.k {
		if v != 0 {
			return false
		}
	}
	return true
}

// Operation 是分支内部的一个顺序子操作（正向生效）。
type Operation interface {
	// Name 用于日志与失败定位。
	Name() string
	// Keys 返回该子操作触碰的全部对象图键。
	Keys() []string
	// Apply 在对象图上生效副作用。返回非 nil 错误表示该子操作失败；
	// 失败时子操作必须保证对象图没有产生任何改动（要么全做，要么不做）。
	Apply(g *Graph) error
}

// Inverse 是已生效子操作的逆操作。
type Inverse interface {
	Name() string
	Keys() []string
	// Undo 撤销此前 Apply 造成的改动。返回非 nil 错误表示逆操作失败。
	Undo(g *Graph) error
}

// Logger 记录每次补偿尝试的输入、结果与判定依据。
// nil 记录器是合法的（表示丢弃日志）。
type Logger interface {
	LogAttempt(entry LogEntry)
}

// LogEntry 是一条补偿尝试日志。
type LogEntry struct {
	ActionName  string
	BranchName  string
	StepIndex   int
	InverseName string
	// Input 描述逆操作输入（触碰键、预期增量等）。
	Input string
	// Allowed 为 false 表示该次尝试在顺序门禁处被拒绝。
	Allowed bool
	// Result 为 "ok" / "undo-error" / "rejected"。
	Result string
	// Reason 是判定依据，例如：
	// "downstream-remaining=0" 或 "blocked-by=[B C]"。
	Reason string
}

// LockManager 提供跨动作的键级互斥。
//
// 为保证"任意两个不同动作的并发执行与补偿等价于某个串行顺序"，
// 采用严格两阶段封锁（strict 2PL）：动作在开始前按键的字典序
// 一次性获取全部触碰键的锁，动作（含补偿）全部结束后统一释放。
// 有序加锁使系统不可能发生加锁环，因此不会死锁；冲突动作在
// LockAll 处串行化，无键交集的动作则完全并行。
type LockManager struct {
	mu     sync.Mutex
	held   map[string]chan struct{}
	owners map[string]uint64
}

// NewLockManager 创建键级锁管理器。
func NewLockManager() *LockManager {
	return &LockManager{
		held:   make(map[string]chan struct{}),
		owners: make(map[string]uint64),
	}
}

// Token 是一次动作持锁集合的凭证。
type Token struct {
	keys []string
	id   uint64
}

// LockAll 按字典序获取给定键集合上的独占锁，返回持锁凭证。
// 阻塞直到所有键可用。同一动作对同一键重复包含只会被去重一次。
func (m *LockManager) LockAll(actionID uint64, keys []string) *Token {
	uniq := dedupSorted(keys)
	for _, key := range uniq {
		for {
			m.mu.Lock()
			ch, busy := m.held[key]
			owner := m.owners[key]
			if !busy {
				m.held[key] = make(chan struct{})
				m.owners[key] = actionID
				m.mu.Unlock()
				break
			}
			m.mu.Unlock()
			// 可重入：同一动作已持有该键时无需等待。
			if owner == actionID {
				break
			}
			<-ch
		}
	}
	return &Token{keys: uniq, id: actionID}
}

// UnlockAll 释放凭证上的全部键锁。
func (m *LockManager) UnlockAll(t *Token) {
	if t == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range t.keys {
		if ch, ok := m.held[key]; ok && m.owners[key] == t.id {
			delete(m.held, key)
			delete(m.owners, key)
			close(ch)
		}
	}
}

func dedupSorted(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := append([]string(nil), keys...)
	sort.Strings(out)
	w := 0
	for i := range out {
		if i > 0 && out[i] == out[i-1] {
			continue
		}
		out[w] = out[i]
		w++
	}
	return out[:w]
}
