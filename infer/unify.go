package infer

import "fmt"

// journalEntry 记录一次变量状态修改前的旧值，用于失败时回滚。
type journalEntry struct {
	id       int
	oldLevel int
	oldBound *Type
}

// Unify 按固定过程递归合一两个类型，以第一个遇到的失败返回。
// 两个类型先完整校验（先左后右），再开始合一；整个操作原子：
// 失败时本次已做的全部绑定与层级调整都撤销。
func (s *Session) Unify(a, b Type) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(a); err != nil {
		return err
	}
	if err := s.validate(b); err != nil {
		return err
	}
	var journal []journalEntry
	if err := s.unify(a, b, &journal); err != nil {
		for i := len(journal) - 1; i >= 0; i-- {
			e := journal[i]
			s.vars[e.id-1].level = e.oldLevel
			s.vars[e.id-1].bound = e.oldBound
		}
		return err
	}
	return nil
}

// unify 是 Unify 的递归体，所有状态修改先记入 journal。调用方须持有锁。
func (s *Session) unify(a, b Type, journal *[]journalEntry) error {
	pa := s.prune(a)
	pb := s.prune(b)
	if pa.IsVar && pb.IsVar && pa.ID == pb.ID {
		return nil
	}
	if pa.IsVar {
		return s.bindVar(pa.ID, pb, journal)
	}
	if pb.IsVar {
		return s.bindVar(pb.ID, pa, journal)
	}
	if pa.Name != pb.Name {
		return fmt.Errorf("infer: unify %s vs %s: %w", pa, pb, ErrMismatch)
	}
	for i := range pa.Args {
		if err := s.unify(pa.Args[i], pb.Args[i], journal); err != nil {
			return err
		}
	}
	return nil
}

// bindVar 把未绑定变量 id 绑定到已 Prune 的类型 t（t 为另一未绑定变量
// 或构造子应用）。调用方须持有锁。
func (s *Session) bindVar(id int, t Type, journal *[]journalEntry) error {
	if t.IsVar {
		s.record(journal, id)
		bound := t
		s.vars[id-1].bound = &bound
		other := &s.vars[t.ID-1]
		if lo := min(other.level, s.vars[id-1].level); other.level != lo {
			s.record(journal, t.ID)
			other.level = lo
		}
		return nil
	}
	resolved := s.resolve(t)
	if occurs(resolved, id) {
		return fmt.Errorf("infer: unify Var(%d) with %s: %w", id, t, ErrOccurs)
	}
	level := s.vars[id-1].level
	for _, u := range collectVars(resolved, nil) {
		if s.vars[u-1].level > level {
			s.record(journal, u)
			s.vars[u-1].level = level
		}
	}
	s.record(journal, id)
	bound := t.clone()
	s.vars[id-1].bound = &bound
	return nil
}

// record 把变量 id 的当前状态追加到 journal。调用方须持有锁。
func (s *Session) record(journal *[]journalEntry, id int) {
	v := s.vars[id-1]
	*journal = append(*journal, journalEntry{id: id, oldLevel: v.level, oldBound: v.bound})
}

// occurs 报告已解析类型 t 中是否出现变量 id。
func occurs(t Type, id int) bool {
	if t.IsVar {
		return t.ID == id
	}
	for _, a := range t.Args {
		if occurs(a, id) {
			return true
		}
	}
	return false
}

// collectVars 按先序收集 t 中全部变量编号（含重复）。
func collectVars(t Type, into []int) []int {
	if t.IsVar {
		return append(into, t.ID)
	}
	for _, a := range t.Args {
		into = collectVars(a, into)
	}
	return into
}
