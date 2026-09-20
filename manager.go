package ontology

import (
	"fmt"
	"maps"
	"sync"
)

// version 是一份不可变的配置内容：字段值 + 每个字段的来源版本号。
type version struct {
	num     uint64
	values  map[string]any
	sources map[string]uint64
}

// Manager 管理配置的版本历史与快照引用生命周期。
// 所有方法都可并发调用。
type Manager struct {
	mu          sync.Mutex
	schema      *Schema
	current     uint64
	next        uint64 // 下一个待分配的版本号，只增不减，永不复用
	versions    map[uint64]*version
	refs        map[uint64]int // 每个版本上未归还的快照数
	outstanding int            // 全部未归还快照数
}

// NewManager 以 initial 为第 1 版内容创建管理器。
// initial 必须为每个已声明字段提供合法值，否则返回错误。
func NewManager(schema *Schema, initial map[string]any) (*Manager, error) {
	for _, name := range schema.Names() {
		v, ok := initial[name]
		if !ok {
			return nil, &ValidationError{Field: name,
				Err: fmt.Errorf("initial value missing")}
		}
		if err := schema.Validate(name, v); err != nil {
			return nil, err
		}
	}
	for name := range initial {
		if !schema.Has(name) {
			return nil, &ValidationError{Field: name, Err: ErrUnknownField}
		}
	}
	values := make(map[string]any, len(initial))
	sources := make(map[string]uint64, len(initial))
	for name, v := range initial {
		values[name] = v
		sources[name] = 1
	}
	m := &Manager{
		schema:   schema,
		current:  1,
		next:     1,
		versions: map[uint64]*version{},
		refs:     map[uint64]int{},
	}
	m.versions[1] = &version{num: 1, values: values, sources: sources}
	return m, nil
}

// Current 返回当前版本号。
func (m *Manager) Current() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// commit 在持锁状态下把 changes 应用为一个新版本。
// 字段级来源版本规则：值发生变化的字段，来源版本前进到新版本号；
// 值不变的字段（包括被更新成相同值）来源版本保持不动。
func (m *Manager) commit(changes map[string]any) uint64 {
	cur := m.versions[m.current]
	values := maps.Clone(cur.values)
	sources := maps.Clone(cur.sources)
	m.next++
	for name, v := range changes {
		if values[name] != v {
			sources[name] = m.next
		}
		values[name] = v
	}
	m.versions[m.next] = &version{num: m.next, values: values, sources: sources}
	m.current = m.next
	return m.next
}

// Update 原子地应用一组字段变更，返回新版本号。
// 全部变更先整体过校验；任何一项不通过，整次更新不生效、
// 版本号不前进，并返回指明字段与类别的 *ValidationError。
func (m *Manager) Update(changes map[string]any) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, v := range changes {
		if err := m.schema.Validate(name, v); err != nil {
			return 0, err
		}
	}
	return m.commit(changes), nil
}

// Restore 把配置内容切换为历史版本 num 的内容，并返回产生的新版本号。
// 版本号严格递增：结果一定大于调用前的当前版本号，历史版本号永不复用。
// num 已被回收时返回 ErrVersionReclaimed，从未存在时返回 ErrUnknownVersion。
func (m *Manager) Restore(num uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, err := m.lookup(num)
	if err != nil {
		return 0, err
	}
	return m.commit(src.values), nil
}

// lookup 在持锁状态下取历史版本，区分"已回收"与"从未存在"。
func (m *Manager) lookup(num uint64) (*version, error) {
	v, ok := m.versions[num]
	if ok {
		return v, nil
	}
	if num >= 1 && num <= m.next {
		return nil, fmt.Errorf("version %d: %w", num, ErrVersionReclaimed)
	}
	return nil, fmt.Errorf("version %d: %w", num, ErrUnknownVersion)
}

// GetAt 读取历史版本 num 中字段 name 的值。
// 版本被回收或字段未声明时返回可判定的错误。
func (m *Manager) GetAt(num uint64, name string) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.lookup(num)
	if err != nil {
		return nil, err
	}
	val, ok := v.values[name]
	if !ok {
		return nil, &ValidationError{Field: name, Err: ErrUnknownField}
	}
	return val, nil
}

// SourceAt 返回历史版本 num 中字段 name 的来源版本号，
// 即该字段当前值是由哪一次更新写入的。
func (m *Manager) SourceAt(num uint64, name string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.lookup(num)
	if err != nil {
		return 0, err
	}
	src, ok := v.sources[name]
	if !ok {
		return 0, &ValidationError{Field: name, Err: ErrUnknownField}
	}
	return src, nil
}
