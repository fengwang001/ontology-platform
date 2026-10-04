package bundle

import (
	"sync"

	"ontology/flowtable"
)

// MaxMsgs 是单个批最多缓存的消息数。
const MaxMsgs = 256

type bnd struct {
	ops []flowtable.Op
}

// Manager 管理全部打开的批。
type Manager struct {
	mu sync.Mutex
	t  *flowtable.FlowTable
	// 批本身由调用方持锁串行使用；这里用同一把锁保护映射。
	bundles map[uint64]*bnd
	nextID  uint64
}

// NewManager 创建批管理器。
func NewManager(t *flowtable.FlowTable) *Manager {
	return &Manager{t: t, bundles: map[uint64]*bnd{}, nextID: 1}
}

// Begin 打开一个新批并返回批号。
func (m *Manager) Begin() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	m.bundles[id] = &bnd{}
	return id
}

// Append 缓存一条消息；参数非法或超出 256 条报 ErrInvalid。
func (m *Manager) Append(id uint64, op flowtable.Op) error {
	switch op.Kind {
	case flowtable.OpAdd:
		if !op.Add.Match.Valid() {
			return flowtable.ErrInvalid
		}
	case flowtable.OpModify:
		if !op.Modify.Match.Valid() {
			return flowtable.ErrInvalid
		}
	case flowtable.OpDelete:
		if !op.Delete.Match.Valid() {
			return flowtable.ErrInvalid
		}
	default:
		return flowtable.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bundles[id]
	if !ok {
		return flowtable.ErrNoBundle
	}
	if len(b.ops) >= MaxMsgs {
		return flowtable.ErrInvalid
	}
	b.ops = append(b.ops, op)
	return nil
}

// Commit 原子提交整批；拒绝时批保持打开。
func (m *Manager) Commit(id uint64, now int64) ([]flowtable.Event, error) {
	m.mu.Lock()
	b, ok := m.bundles[id]
	if !ok {
		m.mu.Unlock()
		return nil, flowtable.ErrNoBundle
	}
	ops := append([]flowtable.Op(nil), b.ops...)
	m.mu.Unlock()

	events, err := m.t.CommitBundle(ops, now)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	delete(m.bundles, id)
	m.mu.Unlock()
	return events, nil
}

// Discard 丢弃一个批。
func (m *Manager) Discard(id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bundles[id]; !ok {
		return flowtable.ErrNoBundle
	}
	delete(m.bundles, id)
	return nil
}
