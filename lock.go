package ontology

import (
	"sort"
	"sync"
)

// Mode 是多粒度意向锁的持锁模式。
type Mode string

const (
	IS  Mode = "IS"
	IX  Mode = "IX"
	S   Mode = "S"
	SIX Mode = "SIX"
	X   Mode = "X"
)

// LockManager 管理若干棵资源树上的多粒度意向锁。
// 所有方法均可被并发调用，其结果等价于某个串行执行顺序。
type LockManager struct {
	mu sync.RWMutex
	// parent 记录每个已登记节点的父节点；根节点的父节点为空串。
	parent map[string]string
	// locks[node][txn] 为该事务在该节点上持有的唯一模式。
	locks map[string]map[int64]Mode
}

// NewLockManager 创建一个空的锁管理器。
func NewLockManager() *LockManager {
	return &LockManager{
		parent: map[string]string{},
		locks:  map[string]map[int64]Mode{},
	}
}

// Register 登记资源节点；parent 为空串表示该节点是一棵新树的根。
func (m *LockManager) Register(id, parent string) error {
	if id == "" {
		return ErrEmptyNodeID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parent[id]; ok {
		return ErrNodeExists
	}
	if parent != "" {
		if _, ok := m.parent[parent]; !ok {
			return ErrParentNotExist
		}
	}
	m.parent[id] = parent
	m.locks[id] = map[int64]Mode{}
	return nil
}

// Lock 尝试让 txn 在 node 上持有 mode（必要时按 join 转换）。
// 返回持锁后的模式；若被拒绝则返回错误且不改变任何状态。
func (m *LockManager) Lock(txn int64, node string, mode Mode) (Mode, error) {
	if txn <= 0 {
		return "", ErrInvalidTxn
	}
	if !validMode[mode] {
		return "", ErrInvalidMode
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	holders, ok := m.locks[node]
	if !ok {
		return "", ErrNodeNotExist
	}

	held, has := holders[txn]
	// 已持有的模式不弱于请求模式：直接成功，且不再检查祖先与冲突。
	if has && atLeast(held, mode) {
		return held, nil
	}

	target := mode
	if has {
		target = join(held, mode)
	}

	// 祖先意向检查：沿父链收集真祖先，再按从根到该节点的顺序检查。
	need := requiredIntent(target)
	chain := m.ancestorsLocked(node)
	for i := len(chain) - 1; i >= 0; i-- {
		anc := chain[i]
		ancHeld, ancHas := m.locks[anc][txn]
		if !ancHas || !atLeast(ancHeld, need) {
			return "", &AncestorIntentError{
				Txn:      txn,
				Ancestor: anc,
				Held:     ancHeld,
				Required: need,
			}
		}
	}

	// 本地相容检查：目标模式须与其他每个事务在该节点上的模式相容。
	var minConflictTxn int64
	var minConflictMode Mode
	for otherTxn, otherMode := range holders {
		if otherTxn == txn {
			continue
		}
		if !canCoexist(target, otherMode) {
			if minConflictTxn == 0 || otherTxn < minConflictTxn {
				minConflictTxn = otherTxn
				minConflictMode = otherMode
			}
		}
	}
	if minConflictTxn != 0 {
		return "", &ConflictError{
			Txn:          txn,
			Node:         node,
			Mode:         target,
			ConflictTxn:  minConflictTxn,
			ConflictMode: minConflictMode,
		}
	}

	holders[txn] = target
	return target, nil
}

// Unlock 释放 txn 在 node 上的锁。
func (m *LockManager) Unlock(txn int64, node string) error {
	if txn <= 0 {
		return ErrInvalidTxn
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	holders, ok := m.locks[node]
	if !ok {
		return ErrNodeNotExist
	}
	if _, has := holders[txn]; !has {
		return ErrLockNotHeld
	}

	// 真后代检查：该事务在任何真后代上仍持锁时拒绝释放。
	descendant, descMode := m.firstDescendantLockLocked(node, txn)
	if descendant != "" {
		return &DescendantLockError{
			Txn:        txn,
			Node:       node,
			Descendant: descendant,
			Mode:       descMode,
		}
	}

	delete(holders, txn)
	return nil
}

// ReleaseAll 一次性释放 txn 持有的全部锁，不暴露任何中间态。
func (m *LockManager) ReleaseAll(txn int64) error {
	if txn <= 0 {
		return ErrInvalidTxn
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, holders := range m.locks {
		delete(holders, txn)
	}
	return nil
}

// Held 返回 txn 在 node 上的持锁模式；未持有时持锁标志为 false。
func (m *LockManager) Held(txn int64, node string) (Mode, bool, error) {
	if txn <= 0 {
		return "", false, ErrInvalidTxn
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	holders, ok := m.locks[node]
	if !ok {
		return "", false, ErrNodeNotExist
	}
	mode, has := holders[txn]
	return mode, has, nil
}

// Holder 表示某个事务在一个节点上的持锁情况。
type Holder struct {
	Txn  int64
	Mode Mode
}

// Holders 按事务号升序返回 node 上的全部持有者。
func (m *LockManager) Holders(node string) ([]Holder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	holders, ok := m.locks[node]
	if !ok {
		return nil, ErrNodeNotExist
	}
	txns := make([]int64, 0, len(holders))
	for txn := range holders {
		txns = append(txns, txn)
	}
	sort.Slice(txns, func(i, j int) bool { return txns[i] < txns[j] })
	result := make([]Holder, 0, len(txns))
	for _, txn := range txns {
		result = append(result, Holder{Txn: txn, Mode: holders[txn]})
	}
	return result, nil
}

// ancestorsLocked 返回 node 的全部真祖先，顺序为从父节点向上到根。
// 调用方须持有写锁；node 必须已登记。
func (m *LockManager) ancestorsLocked(node string) []string {
	var chain []string
	for p := m.parent[node]; p != ""; p = m.parent[p] {
		chain = append(chain, p)
	}
	return chain
}

// firstDescendantLockLocked 查找 txn 在 node 的任意真后代上的持锁。
// 为保证相同操作序列重放结果完全一致，返回节点 id 最小的那个后代。
// 调用方须持有写锁；node 必须已登记。
func (m *LockManager) firstDescendantLockLocked(node string, txn int64) (string, Mode) {
	var descendants []string
	for cand, parent := range m.parent {
		if cand == node {
			continue
		}
		for p := parent; p != ""; p = m.parent[p] {
			if p == node {
				descendants = append(descendants, cand)
				break
			}
		}
	}
	sort.Strings(descendants)
	for _, desc := range descendants {
		if mode, has := m.locks[desc][txn]; has {
			return desc, mode
		}
	}
	return "", ""
}
