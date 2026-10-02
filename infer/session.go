// Package infer 实现带层级泛化与值限制的 Hindley-Milner 合一推断会话。
//
// 会话维护当前层级 L、单调递增的变量编号、每个变量的层级与绑定，
// 以及一个名字到类型模式的环境。所有导出方法都可并发调用，
// 效果等价于某个串行顺序；一次 Unify 是不可分割的原子步骤。
package infer

import (
	"fmt"
	"sync"
)

const (
	maxLevel     = 1000
	maxEnvSize   = 1000
	maxArity     = 4
	maxNameBytes = 32
	maxCtors     = 16
	maxVarLimit  = 1_000_000
)

// varInfo 记录一个变量的层级与绑定；bound 为 nil 表示未绑定。
type varInfo struct {
	level int
	bound *Type
}

// scheme 是一个类型模式：body 中第 i 个量化变量编码为 Var(-(i+1))，
// 未量化部分保留原变量编号，与原变量共享。
type scheme struct {
	body  Type
	quant int
}

// Session 是一个合一推断会话。零值不可用，请用 NewSession 构造。
type Session struct {
	mu     sync.Mutex
	ctors  map[string]int
	maxVar int
	level  int
	nextID int
	vars   []varInfo
	env    map[string]scheme
}

// NewSession 以构造子表（名字到元数）与变量上限 maxVar 创建会话。
// 构造子名字须为 1 到 32 字节的非空串，元数 0 到 4，
// 构造子个数 1 到 16，maxVar 为 1 到 10^6，否则返回 ErrInvalidArgument。
func NewSession(ctors map[string]int, maxVar int) (*Session, error) {
	if len(ctors) < 1 || len(ctors) > maxCtors {
		return nil, fmt.Errorf("infer: constructor count %d: %w", len(ctors), ErrInvalidArgument)
	}
	table := make(map[string]int, len(ctors))
	for name, arity := range ctors {
		if len(name) < 1 || len(name) > maxNameBytes {
			return nil, fmt.Errorf("infer: constructor name %q: %w", name, ErrInvalidArgument)
		}
		if arity < 0 || arity > maxArity {
			return nil, fmt.Errorf("infer: constructor %q arity %d: %w", name, arity, ErrInvalidArgument)
		}
		table[name] = arity
	}
	if maxVar < 1 || maxVar > maxVarLimit {
		return nil, fmt.Errorf("infer: variable limit %d: %w", maxVar, ErrInvalidArgument)
	}
	return &Session{
		ctors:  table,
		maxVar: maxVar,
		nextID: 1,
		env:    make(map[string]scheme),
	}, nil
}

// NewVar 返回编号恰为下一个编号、层级为当前 L 的新变量。
// 变量总数已达 V 时返回 ErrVarLimit，且不消耗编号。
func (s *Session) NewVar() (Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.vars) >= s.maxVar {
		return Type{}, fmt.Errorf("infer: newvar: %w", ErrVarLimit)
	}
	id := s.nextID
	s.nextID++
	s.vars = append(s.vars, varInfo{level: s.level})
	return Var(id), nil
}

// Enter 令当前层级加一；L 已为 1000 时返回 ErrLevelLimit。
func (s *Session) Enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.level >= maxLevel {
		return fmt.Errorf("infer: enter: %w", ErrLevelLimit)
	}
	s.level++
	return nil
}

// Leave 令当前层级减一；L 为 0 时返回 ErrLevelZero。
func (s *Session) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.level == 0 {
		return fmt.Errorf("infer: leave: %w", ErrLevelZero)
	}
	s.level--
	return nil
}

// Level 返回变量 id 经 Prune 后若为未绑定变量的层级；
// 若已绑定到构造子应用则返回 ErrBound；编号未知返回 ErrInvalidArgument。
func (s *Session) Level(id int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > len(s.vars) {
		return 0, fmt.Errorf("infer: level: variable %d: %w", id, ErrInvalidArgument)
	}
	p := s.prune(Var(id))
	if !p.IsVar {
		return 0, fmt.Errorf("infer: level: variable %d: %w", id, ErrBound)
	}
	return s.vars[p.ID-1].level, nil
}

// Resolve 返回类型的完全解析形式：所有已绑定变量被递归替换为其绑定。
// 类型非法（未登记构造子、元数不符、未知变量编号）时返回 ErrInvalidArgument。
func (s *Session) Resolve(t Type) (Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(t); err != nil {
		return Type{}, err
	}
	return s.resolve(t), nil
}

// prune 沿绑定链走到未绑定变量或构造子应用。调用方须持有锁。
func (s *Session) prune(t Type) Type {
	for t.IsVar {
		b := s.vars[t.ID-1].bound
		if b == nil {
			return t
		}
		t = *b
	}
	return t
}

// resolve 返回 t 的完全解析形式（深拷贝）。调用方须持有锁。
func (s *Session) resolve(t Type) Type {
	p := s.prune(t)
	if p.IsVar {
		return p
	}
	args := make([]Type, len(p.Args))
	for i, a := range p.Args {
		args[i] = s.resolve(a)
	}
	return Type{Name: p.Name, Args: args}
}

// validate 校验类型项：变量编号已知、构造子已登记且元数相符。
// 空构造子名字因未登记同样非法。调用方须持有锁。
func (s *Session) validate(t Type) error {
	if t.IsVar {
		if t.ID < 1 || t.ID > len(s.vars) {
			return fmt.Errorf("infer: unknown variable %d: %w", t.ID, ErrInvalidArgument)
		}
		return nil
	}
	arity, ok := s.ctors[t.Name]
	if !ok {
		return fmt.Errorf("infer: unknown constructor %q: %w", t.Name, ErrInvalidArgument)
	}
	if len(t.Args) != arity {
		return fmt.Errorf("infer: constructor %q arity %d, got %d args: %w",
			t.Name, arity, len(t.Args), ErrInvalidArgument)
	}
	for _, a := range t.Args {
		if err := s.validate(a); err != nil {
			return err
		}
	}
	return nil
}
