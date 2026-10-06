// Package alias 实现别名成员关系维护与一批别名动作的全有或全无提交。
package alias

import (
	"errors"
	"fmt"

	"ontology/indexreg"
)

var (
	ErrMemberNotFound       = errors.New("alias: member not found")
	ErrMultipleWriteIndices = errors.New("alias: multiple write indices")
)

// MultiWriteError 携带终态含多个 isWrite=真 成员的字节序最小别名。
type MultiWriteError struct{ Alias string }

func (e *MultiWriteError) Error() string {
	return fmt.Sprintf("alias: alias %q has multiple write indices", e.Alias)
}

func (e *MultiWriteError) Is(err error) bool { return err == ErrMultipleWriteIndices }

// StepError 携带推演失败的动作下标（取下标最小者）。
type StepError struct {
	Index int
	Err   error
}

func (e *StepError) Error() string { return fmt.Sprintf("alias: action %d: %v", e.Index, e.Err) }

func (e *StepError) Unwrap() error { return e.Err }

// Kind 是动作种类。
type Kind int8

const (
	KindAdd Kind = iota
	KindRemove
	KindRemoveIndex
)

// Action 是一个别名动作；字段按 Kind 取用。
type Action struct {
	Kind      Kind
	Alias     string             // KindAdd, KindRemove
	Index     string             // 三种均用
	IsWrite   indexreg.WriteFlag // KindAdd
	Filter    *string            // KindAdd，可空
	MustExist bool               // KindRemove
}

func Add(alias, index string, isWrite indexreg.WriteFlag, filter *string) Action {
	return Action{Kind: KindAdd, Alias: alias, Index: index, IsWrite: isWrite, Filter: filter}
}

func Remove(alias, index string, mustExist bool) Action {
	return Action{Kind: KindRemove, Alias: alias, Index: index, MustExist: mustExist}
}

func RemoveIndex(index string) Action {
	return Action{Kind: KindRemoveIndex, Index: index}
}

// Update 原子提交 1..100 个动作。拒绝次序：参数非法 > 逐步错误（下标最小者）
// > 终态名字冲突（字节序最小名字）> 终态多写索引（字节序最小别名）。
// 被拒绝则状态与纪元都不变；终态与提交前完全相同的批被接受但不加纪元。
func Update(r *indexreg.Registry, actions []Action) error {
	if len(actions) < 1 || len(actions) > 100 {
		return fmt.Errorf("alias: need 1..100 actions, got %d: %w", len(actions), indexreg.ErrInvalidArgument)
	}
	for i, a := range actions {
		if err := validateAction(a); err != nil {
			return fmt.Errorf("alias: action %d: %w", i, err)
		}
	}
	return r.Mutate(func(st *indexreg.State) (bool, error) {
		work := cloneState(st)
		for i, a := range actions {
			if err := step(work, a); err != nil {
				return false, &StepError{Index: i, Err: err}
			}
		}
		if name, ok := nameConflict(work); ok {
			return false, &indexreg.ConflictError{Name: name}
		}
		if name, ok := multiWrite(work); ok {
			return false, &MultiWriteError{Alias: name}
		}
		if statesEqual(st, work) {
			return false, nil
		}
		deriveAll(work)
		*st = *work
		return true, nil
	})
}

func validateAction(a Action) error {
	bad := func(what, name string) error {
		return fmt.Errorf("alias: %s name %q: %w", what, name, indexreg.ErrInvalidArgument)
	}
	switch a.Kind {
	case KindAdd, KindRemove:
		if !indexreg.ValidName(a.Alias) {
			return bad("alias", a.Alias)
		}
		if !indexreg.ValidName(a.Index) {
			return bad("index", a.Index)
		}
	case KindRemoveIndex:
		if !indexreg.ValidName(a.Index) {
			return bad("index", a.Index)
		}
	default:
		return fmt.Errorf("alias: unknown action kind %d: %w", a.Kind, indexreg.ErrInvalidArgument)
	}
	return nil
}

// step 在工作副本上应用一个动作，只做三类逐步检查。
func step(st *indexreg.State, a Action) error {
	switch a.Kind {
	case KindAdd:
		if _, ok := st.Indices[a.Index]; !ok {
			return fmt.Errorf("alias: index %q: %w", a.Index, indexreg.ErrIndexNotFound)
		}
		al := st.Aliases[a.Alias]
		if al == nil {
			al = &indexreg.Alias{Members: make(map[string]indexreg.Member)}
			st.Aliases[a.Alias] = al
		}
		al.Members[a.Index] = indexreg.Member{IsWrite: a.IsWrite, Filter: cloneStr(a.Filter)}
	case KindRemove:
		al := st.Aliases[a.Alias]
		if al == nil {
			if a.MustExist {
				return memberNotFound(a)
			}
			return nil
		}
		if _, ok := al.Members[a.Index]; !ok {
			if a.MustExist {
				return memberNotFound(a)
			}
			return nil
		}
		delete(al.Members, a.Index)
		if len(al.Members) == 0 {
			delete(st.Aliases, a.Alias)
		}
	case KindRemoveIndex:
		if _, ok := st.Indices[a.Index]; !ok {
			return fmt.Errorf("alias: index %q: %w", a.Index, indexreg.ErrIndexNotFound)
		}
		delete(st.Indices, a.Index)
		for name, al := range st.Aliases {
			delete(al.Members, a.Index)
			if len(al.Members) == 0 {
				delete(st.Aliases, name)
			}
		}
	}
	return nil
}

func memberNotFound(a Action) error {
	return fmt.Errorf("alias: member (%q,%q): %w", a.Alias, a.Index, ErrMemberNotFound)
}

// nameConflict 返回终态中既是索引又是别名的字节序最小名字。
func nameConflict(st *indexreg.State) (string, bool) {
	best := ""
	for name := range st.Aliases {
		if _, ok := st.Indices[name]; !ok {
			continue
		}
		if best == "" || name < best {
			best = name
		}
	}
	return best, best != ""
}

// multiWrite 返回终态中含多个 isWrite=真 成员的字节序最小别名。
func multiWrite(st *indexreg.State) (string, bool) {
	best := ""
	for name, al := range st.Aliases {
		n := 0
		for _, m := range al.Members {
			if m.IsWrite == indexreg.WriteTrue {
				n++
			}
		}
		if n > 1 && (best == "" || name < best) {
			best = name
		}
	}
	return best, best != ""
}

// deriveAll 为每个别名重推写索引并缓存，供 resolve 以 O(1) 成员触碰读取。
func deriveAll(st *indexreg.State) {
	for _, al := range st.Aliases {
		al.HasWrite, al.WriteIndex = deriveWrite(al)
	}
}

// deriveWrite 推定写索引：恰一个 isWrite=真 取它；否则恰一个成员且未指定取它；
// 其余情况（含单成员显式假、多成员都未指定）无写索引。
func deriveWrite(al *indexreg.Alias) (bool, string) {
	nTrue := 0
	write := ""
	for idx, m := range al.Members {
		if m.IsWrite == indexreg.WriteTrue {
			nTrue++
			write = idx
		}
	}
	if nTrue == 1 {
		return true, write
	}
	if nTrue == 0 && len(al.Members) == 1 {
		for idx, m := range al.Members {
			if m.IsWrite == indexreg.WriteUnspecified {
				return true, idx
			}
		}
	}
	return false, ""
}

func cloneState(st *indexreg.State) *indexreg.State {
	out := &indexreg.State{
		Indices: make(map[string]*indexreg.Index, len(st.Indices)),
		Aliases: make(map[string]*indexreg.Alias, len(st.Aliases)),
	}
	for name, idx := range st.Indices {
		cp := *idx
		out.Indices[name] = &cp
	}
	for name, al := range st.Aliases {
		cp := &indexreg.Alias{
			Members:    make(map[string]indexreg.Member, len(al.Members)),
			HasWrite:   al.HasWrite,
			WriteIndex: al.WriteIndex,
		}
		for idx, m := range al.Members {
			cp.Members[idx] = indexreg.Member{IsWrite: m.IsWrite, Filter: cloneStr(m.Filter)}
		}
		out.Aliases[name] = cp
	}
	return out
}

// statesEqual 深比较两份状态（忽略由成员确定性推出的缓存字段）。
func statesEqual(a, b *indexreg.State) bool {
	if len(a.Indices) != len(b.Indices) || len(a.Aliases) != len(b.Aliases) {
		return false
	}
	for name, ia := range a.Indices {
		ib, ok := b.Indices[name]
		if !ok || ia.Closed != ib.Closed {
			return false
		}
	}
	for name, aa := range a.Aliases {
		ab, ok := b.Aliases[name]
		if !ok || len(aa.Members) != len(ab.Members) {
			return false
		}
		for idx, ma := range aa.Members {
			mb, ok := ab.Members[idx]
			if !ok || ma.IsWrite != mb.IsWrite || !strEq(ma.Filter, mb.Filter) {
				return false
			}
		}
	}
	return true
}

func cloneStr(s *string) *string {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
