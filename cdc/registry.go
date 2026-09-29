package cdc

import (
	"fmt"
	"sync"
)

// Registry 模式注册表，保存所有已注册版本的不可变快照。
// 所有方法均可并发调用；演进操作采用写时复制，
// 进行中的投影始终基于某一完整版本，不会看到新旧混合的模式。
type Registry struct {
	mu      sync.RWMutex
	schemas map[int]*Schema
	next    int
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{schemas: make(map[int]*Schema), next: 1}
}

// Register 校验并注册一个完整模式，版本号由注册表分配并返回。
// 校验失败时注册表保持不变。
func (r *Registry) Register(cols []Column) (int, error) {
	if err := validateColumns(cols); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commitLocked(cols), nil
}

// AddColumn 在指定版本基础上新增一列，生成新版本。
// 基版本不存在、列非法或列名已存在时拒绝，注册表保持不变。
func (r *Registry) AddColumn(version int, col Column) (int, error) {
	if err := validateColumn(col); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	base, ok := r.schemas[version]
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrVersionNotFound, version)
	}
	for _, c := range base.Columns {
		if c.Name == col.Name {
			return 0, fmt.Errorf("%w: %q", ErrColumnExists, col.Name)
		}
	}
	cols := make([]Column, 0, len(base.Columns)+1)
	cols = append(cols, base.Columns...)
	cols = append(cols, col)
	return r.commitLocked(cols), nil
}

// DropColumn 在指定版本基础上删除一列，生成新版本。
// 基版本或列不存在时拒绝，注册表保持不变。
func (r *Registry) DropColumn(version int, name string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, ok := r.schemas[version]
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrVersionNotFound, version)
	}
	idx := -1
	for i, c := range base.Columns {
		if c.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, fmt.Errorf("%w: %q", ErrColumnNotFound, name)
	}
	cols := make([]Column, 0, len(base.Columns)-1)
	cols = append(cols, base.Columns[:idx]...)
	cols = append(cols, base.Columns[idx+1:]...)
	return r.commitLocked(cols), nil
}

// AlterColumn 在指定版本基础上修改一列（类型/必填），生成新版本。
// 基版本或列不存在、新列定义非法时拒绝，注册表保持不变。
func (r *Registry) AlterColumn(version int, col Column) (int, error) {
	if err := validateColumn(col); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	base, ok := r.schemas[version]
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrVersionNotFound, version)
	}
	cols := make([]Column, len(base.Columns))
	copy(cols, base.Columns)
	for i, c := range cols {
		if c.Name == col.Name {
			cols[i] = col
			return r.commitLocked(cols), nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrColumnNotFound, col.Name)
}

// Get 返回指定版本模式的深拷贝快照。
func (r *Registry) Get(version int) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[version]
	if !ok {
		return Schema{}, false
	}
	cols := make([]Column, len(s.Columns))
	copy(cols, s.Columns)
	return Schema{Version: s.Version, Columns: cols}, true
}

// commitLocked 在持锁状态下登记一个不可变快照并返回其版本号。
func (r *Registry) commitLocked(cols []Column) int {
	frozen := make([]Column, len(cols))
	copy(frozen, cols)
	v := r.next
	r.schemas[v] = &Schema{Version: v, Columns: frozen}
	r.next++
	return v
}

// validateColumn 校验单列定义。
func validateColumn(c Column) error {
	if c.Name == "" {
		return ErrEmptyColumnName
	}
	if !c.Type.Valid() {
		return fmt.Errorf("%w: %d", ErrInvalidColumnType, int(c.Type))
	}
	return nil
}

// validateColumns 校验整组列定义：逐列合法且无重名。
func validateColumns(cols []Column) error {
	seen := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		if err := validateColumn(c); err != nil {
			return err
		}
		if _, dup := seen[c.Name]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateColumn, c.Name)
		}
		seen[c.Name] = struct{}{}
	}
	return nil
}
