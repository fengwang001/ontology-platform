// Package table 对外符号表：Enter/Leave/Declare/Ref/Captures/SelfCheck。
package table

import (
	"errors"
	"sync"

	"ontology/capture"
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

var (
	// 以下哨兵错误互不相同，均可用 errors.Is 判定。
	ErrEmptyName  = errors.New("table: empty name")                        // 空名字
	ErrDepthLimit = errors.New("table: nesting depth limit exceeded")      // Enter 超深
	ErrDeclLimit  = errors.New("table: declaration count limit exceeded")  // 单层声明超限
	ErrLeaveRoot  = errors.New("table: leave of outermost scope")          // 最外层 Leave
	ErrSelfChain  = errors.New("table: self-check: scope chain corrupt")   // 链有环/深度不配对
	ErrSelfRef    = errors.New("table: self-check: recorded ref unstable") // 已记录引用漂移
)

// Table 是对外符号表。读操作（Ref/Captures/SelfCheck）可并发。
type Table struct {
	mu       sync.RWMutex
	root     *scope.Scope
	cur      *scope.Scope
	maxDepth int
	maxDecls int
	res      *resolve.Resolver

	refsMu  sync.Mutex
	refs    []resolve.Ref
	results []resolve.Result
}

// New 构造符号表；maxDepth 为最大嵌套深度，maxDecls 为单层声明上限。
func New(maxDepth, maxDecls int) *Table {
	root := scope.New(nil, 0)
	return &Table{root: root, cur: root, maxDepth: maxDepth, maxDecls: maxDecls, res: resolve.NewResolver()}
}

// Looked 返回最近一次解析查看的声明条目数。
func (t *Table) Looked() int64 { return t.res.Looked() }

// Enter 进入一层内层作用域；超深拒绝且不留半进入的作用域。
func (t *Table) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur.Depth()+1 > t.maxDepth {
		return ErrDepthLimit
	}
	t.cur = scope.New(t.cur, t.cur.Depth()+1)
	return nil
}

// Leave 离开当前作用域；最外层拒绝。遮蔽靠链式查找实现，外层从不被修改。
func (t *Table) Leave() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == t.root {
		return ErrLeaveRoot
	}
	t.cur = t.cur.Parent()
	return nil
}

// Declare 在当前层声明名字；空名、超层上限、同层重名分别报不同错误。
func (t *Table) Declare(n name.Name, k name.Kind, pos int) error {
	if !n.Valid() {
		return ErrEmptyName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur.Len() >= t.maxDecls {
		return ErrDeclLimit
	}
	return t.cur.Declare(name.Decl{Name: n, Kind: k, Pos: pos})
}

// Ref 从当前作用域解析名字；成功则记录该引用点供 Captures/SelfCheck 使用。
func (t *Table) Ref(n name.Name, pos int) (resolve.Result, error) {
	if !n.Valid() {
		return resolve.Result{}, ErrEmptyName
	}
	t.mu.RLock()
	ref := resolve.Ref{Scope: t.cur, Name: n, Pos: pos}
	t.mu.RUnlock()
	r, err := t.res.Resolve(ref)
	if err != nil {
		return r, err
	}
	t.refsMu.Lock()
	t.refs = append(t.refs, ref)
	t.results = append(t.results, r)
	t.refsMu.Unlock()
	return r, nil
}

// Captures 列出当前作用域引用到的外层声明，逐个列出、去重。
func (t *Table) Captures() ([]capture.Entry, error) {
	t.mu.RLock()
	cur := t.cur
	t.mu.RUnlock()
	t.refsMu.Lock()
	refs := append([]resolve.Ref(nil), t.refs...)
	t.refsMu.Unlock()
	return capture.Of(cur, refs, t.res)
}

// SelfCheck 核验：链无环且深度配对、每层无重复名字、已记录引用仍稳定。
func (t *Table) SelfCheck() error {
	t.mu.RLock()
	cur, root := t.cur, t.root
	t.mu.RUnlock()
	steps, last := 0, cur
	for s := cur; s != nil; s = s.Parent() {
		if s.Depth() != cur.Depth()-steps || steps > cur.Depth() {
			return ErrSelfChain
		}
		for k, d := range s.Decls() {
			if k != d.Name {
				return ErrSelfChain
			}
		}
		steps++
		last = s
	}
	if last != root {
		return ErrSelfChain
	}
	t.refsMu.Lock()
	refs := append([]resolve.Ref(nil), t.refs...)
	results := append([]resolve.Result(nil), t.results...)
	t.refsMu.Unlock()
	for i, ref := range refs {
		got, err := t.res.Resolve(ref)
		if err != nil || got != results[i] {
			return ErrSelfRef
		}
	}
	return nil
}
