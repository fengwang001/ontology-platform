// Package indexreg 持有索引登记、开闭状态、别名数据与全局纪元，
// 是整个切换器唯一的状态持有者与并发串行化点。
package indexreg

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("indexreg: invalid argument")
	ErrNameConflict    = errors.New("indexreg: name conflict")
	ErrIndexNotFound   = errors.New("indexreg: index not found")
	ErrIndexClosed     = errors.New("indexreg: index closed")
	ErrNameNotFound    = errors.New("indexreg: name not found")
)

// ConflictError 携带冲突的名字（索引与别名共用一个名字空间）。
type ConflictError struct{ Name string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("indexreg: name %q is both index and alias", e.Name)
}

func (e *ConflictError) Is(err error) bool { return err == ErrNameConflict }

// WriteFlag 是成员写标记的三态：未指定、真、假。
type WriteFlag int8

const (
	WriteUnspecified WriteFlag = iota
	WriteTrue
	WriteFalse
)

// Member 是一条别名成员记录：(索引, isWrite, filter)。
type Member struct {
	IsWrite WriteFlag
	Filter  *string
}

// Alias 是一个别名的成员表与提交时缓存的写索引推定结果。
type Alias struct {
	Members    map[string]Member
	HasWrite   bool
	WriteIndex string
}

// Index 是一个已登记索引的开闭状态。
type Index struct {
	Closed bool
}

// State 是全部可变状态，仅在 Registry 锁内经由 Mutate/View 访问。
type State struct {
	Indices map[string]*Index
	Aliases map[string]*Alias
}

// Registry 串行化所有操作；结果等价于某个串行顺序。
type Registry struct {
	mu    sync.RWMutex
	st    State
	epoch uint64
}

func New() *Registry {
	return &Registry{st: State{
		Indices: make(map[string]*Index),
		Aliases: make(map[string]*Alias),
	}}
}

// ValidName 校验 1..64 字节的小写字母/数字/连字符/下划线，
// 且不以连字符或下划线开头。
func ValidName(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return s[0] != '-' && s[0] != '_'
}

// Epoch 返回当前纪元；只有确实改变状态的被接受操作才使其加一。
func (r *Registry) Epoch() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.epoch
}

// CreateIndex 拒绝次序：参数非法 > 与现有索引或别名同名冲突。
func (r *Registry) CreateIndex(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("indexreg: index name %q: %w", name, ErrInvalidArgument)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.st.Indices[name]; ok {
		return &ConflictError{Name: name}
	}
	if _, ok := r.st.Aliases[name]; ok {
		return &ConflictError{Name: name}
	}
	r.st.Indices[name] = &Index{}
	r.epoch++
	return nil
}

func (r *Registry) CloseIndex(name string) error { return r.setClosed(name, true) }

func (r *Registry) OpenIndex(name string) error { return r.setClosed(name, false) }

// setClosed 对不存在的索引报 ErrIndexNotFound；状态本已如此则为空操作（不加纪元）。
func (r *Registry) setClosed(name string, closed bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx, ok := r.st.Indices[name]
	if !ok {
		return fmt.Errorf("indexreg: index %q: %w", name, ErrIndexNotFound)
	}
	if idx.Closed == closed {
		return nil
	}
	idx.Closed = closed
	r.epoch++
	return nil
}

// Mutate 在写锁内执行 fn；fn 返回 changed=true 且无错时纪元加一。
// fn 应在工作副本上推演，仅在确认提交时整体替换 *State。
func (r *Registry) Mutate(fn func(*State) (changed bool, err error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed, err := fn(&r.st)
	if err != nil {
		return err
	}
	if changed {
		r.epoch++
	}
	return nil
}

// View 在读锁内执行 fn，使解析只见某次提交之前或之后的完整状态。
func (r *Registry) View(fn func(*State)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn(&r.st)
}
