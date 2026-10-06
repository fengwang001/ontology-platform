package inline

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

var (
	// ErrBusy 表示决策任务进行期间对输入的修改请求被拒绝。
	ErrBusy = errors.New("inline: registry locked by a running decision task")
	// ErrDuplicateFunc 表示重复登记同名函数。
	ErrDuplicateFunc = errors.New("inline: duplicate function")
)

// Marks 是函数上的内联相关标记。NoInline 与 AlwaysInline 互斥。
type Marks struct {
	NoInline              bool
	AlwaysInline          bool
	NonInlinableStructure bool
}

// CallSite 是函数体内的一个调用点，按出现位置有序。
// Hotness 为热度比例，取值 [0,1]。
type CallSite struct {
	Callee  string
	Hotness float64
}

// Function 是被登记的函数。Size 为初始尺寸。
type Function struct {
	Name      string
	Size      int64
	Marks     Marks
	CallSites []CallSite
}

func (f Function) validate() error {
	if f.Name == "" {
		return errors.New("inline: function name is empty")
	}
	if f.Size < 0 {
		return fmt.Errorf("inline: function %q has negative size %d", f.Name, f.Size)
	}
	if f.Marks.NoInline && f.Marks.AlwaysInline {
		return fmt.Errorf("inline: function %q has both NoInline and AlwaysInline", f.Name)
	}
	for i, cs := range f.CallSites {
		if cs.Callee == "" {
			return fmt.Errorf("inline: function %q call site %d has empty callee", f.Name, i)
		}
		if cs.Hotness < 0 || cs.Hotness > 1 {
			return fmt.Errorf("inline: function %q call site %d hotness %v out of [0,1]", f.Name, i, cs.Hotness)
		}
	}
	return nil
}

// Program 是某一时刻一致的调用关系快照，只读。
type Program struct {
	order []string
	funcs map[string]Function
}

// Lookup 按名取函数。
func (p *Program) Lookup(name string) (Function, bool) {
	f, ok := p.funcs[name]
	return f, ok
}

// Names 返回按登记顺序排列的函数名。
func (p *Program) Names() []string { return slices.Clone(p.order) }

// Registry 是调用关系登记表。它支持并发决策会话：
// 任一会话存活期间，Add/Upsert 一律返回 ErrBusy；
// 会话全部关闭后，修改才被接受并对之后的会话生效。
type Registry struct {
	mu     sync.Mutex
	active int
	order  []string
	funcs  map[string]Function
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{funcs: make(map[string]Function)}
}

// Add 登记新函数，重名报错。
func (r *Registry) Add(f Function) error { return r.modify(f, false) }

// Upsert 登记或替换函数。
func (r *Registry) Upsert(f Function) error { return r.modify(f, true) }

func (r *Registry) modify(f Function, replace bool) error {
	if err := f.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active > 0 {
		return ErrBusy
	}
	_, exists := r.funcs[f.Name]
	if exists && !replace {
		return fmt.Errorf("%w: %q", ErrDuplicateFunc, f.Name)
	}
	if !exists {
		r.order = append(r.order, f.Name)
	}
	r.funcs[f.Name] = f
	return nil
}

// Begin 开启一个决策会话，返回该时刻的一致性快照。
// 会话之间互不影响；会话关闭前修改请求被拒绝。
func (r *Registry) Begin() *Session {
	r.mu.Lock()
	r.active++
	prog := &Program{
		order: slices.Clone(r.order),
		funcs: maps.Clone(r.funcs),
	}
	r.mu.Unlock()
	return &Session{reg: r, prog: prog}
}

// Session 是一次决策任务的句柄，持有输入快照。
type Session struct {
	reg    *Registry
	prog   *Program
	closed atomic.Bool
}

// Program 返回会话快照。
func (s *Session) Program() *Program { return s.prog }

// Close 结束会话，允许后续修改请求生效。幂等。
func (s *Session) Close() {
	if s.closed.CompareAndSwap(false, true) {
		s.reg.mu.Lock()
		s.reg.active--
		s.reg.mu.Unlock()
	}
}
