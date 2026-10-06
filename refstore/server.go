package refstore

import "sync"

// Server 是引用更新事务处理器。
// Push 在写锁内完成整批裁决与应用，等价于某种串行顺序；
// Get/Audit 在读锁内执行，只能看到某一批完整提交前或后的状态。
type Server struct {
	mu    sync.RWMutex
	refs  map[string]string
	graph *Graph
	index *RuleIndex
	audit []AuditRecord
}

func NewServer(g *Graph) *Server {
	return &Server{refs: make(map[string]string), graph: g, index: NewRuleIndex(nil)}
}

// SetRules 整体替换受保护规则。与 Push 互斥：
// 规则只在裁决时读取，裁决期间的规则变更不会影响该批。
func (s *Server) SetRules(rules []Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index = NewRuleIndex(rules)
}

// Get 读取引用当前值。
func (s *Server) Get(ref string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.refs[ref]
	return v, ok
}

// Refs 返回全部引用的快照。
func (s *Server) Refs() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.refs))
	for k, v := range s.refs {
		out[k] = v
	}
	return out
}

// Audit 返回审计记录快照，序号严格递增无空洞。
func (s *Server) Audit() []AuditRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]AuditRecord(nil), s.audit...)
}

// Push 裁决并应用一批更新指令。任一指令被拒则整批拒绝，
// 不改变任何引用、序号与审计记录。
func (s *Server) Push(user string, ins []Instruction) BatchResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := BatchResult{Verdicts: make([]Verdict, len(ins))}
	failed := false
	for i := range ins {
		res.Verdicts[i] = s.adjudicate(user, ins[i])
		if !res.Verdicts[i].OK {
			failed = true
		}
	}
	if !failed {
		// 逐条全部通过后，再做批内一致性检查。
		s.checkBatch(ins, res.Verdicts)
		for _, v := range res.Verdicts {
			if !v.OK {
				failed = true
				break
			}
		}
	}
	if failed {
		// 被整批连坐的通过项标记为「因他项拒绝而未执行」。
		for i := range res.Verdicts {
			if res.Verdicts[i].OK {
				res.Verdicts[i] = Verdict{Reason: ReasonNotExecuted}
			}
		}
		return res
	}

	// 整批原子应用：持锁期间全部写入，读者看不到中间状态。
	rec := AuditRecord{Seq: uint64(len(s.audit)) + 1, User: user}
	for _, in := range ins {
		rec.Updates = append(rec.Updates, RefUpdate{Ref: in.Ref, Old: s.refs[in.Ref], New: in.New})
		if in.New == "" {
			delete(s.refs, in.Ref)
		} else {
			s.refs[in.Ref] = in.New
		}
	}
	s.audit = append(s.audit, rec)
	res.Applied = true
	res.AuditSeq = rec.Seq
	return res
}

// adjudicate 按原因优先级逐条独立判定，只报告最靠前的一个原因。
func (s *Server) adjudicate(user string, in Instruction) Verdict {
	fail := func(r Reason) Verdict { return Verdict{Reason: r} }

	if in.Old == "" && in.New == "" {
		return fail(ReasonInvalidArgument)
	}
	ns, _, ok := splitNamespace(in.Ref)
	if !ok {
		return fail(ReasonInvalidArgument)
	}
	if !validRefName(in.Ref) {
		return fail(ReasonInvalidRefName)
	}
	if in.New != "" && !s.graph.Has(in.New) {
		return fail(ReasonObjectNotFound)
	}
	if s.refs[in.Ref] != in.Old { // 引用不存在时当前值视为空
		return fail(ReasonOldValueMismatch)
	}
	if in.Old == in.New {
		return Verdict{OK: true} // 新旧值相等：无操作，不触发保护与标签检查
	}
	rest := s.index.Match(in.Ref)
	switch {
	case in.Old == "" && rest.NoCreate:
		return fail(ReasonProtectedNoCreate)
	case in.New == "" && rest.NoDelete:
		return fail(ReasonProtectedNoDelete)
	case !rest.Allows(user):
		return fail(ReasonProtectedAllowlist)
	}
	if ns == nsTag {
		if in.Old != "" && in.New != "" {
			return fail(ReasonTagNoUpdate)
		}
		return Verdict{OK: true} // 标签可创建、可删除
	}
	if in.Old != "" && in.New != "" && !s.graph.IsDescendantOrEqual(in.Old, in.New) {
		if rest.NoNonFF {
			return fail(ReasonProtectedNoNonFF)
		}
		if !in.Force {
			return fail(ReasonNonFastForward)
		}
	}
	return Verdict{OK: true}
}

// checkBatch 做批内一致性检查，只标记直接构成批内冲突的指令。
func (s *Server) checkBatch(ins []Instruction, verdicts []Verdict) {
	mark := func(i int) { verdicts[i] = Verdict{Reason: ReasonBatchConflict} }

	// 同一引用在批内出现两次（含标签删除与重建同批）。
	byRef := make(map[string][]int, len(ins))
	for i, in := range ins {
		byRef[in.Ref] = append(byRef[in.Ref], i)
	}
	for _, idxs := range byRef {
		if len(idxs) > 1 {
			for _, i := range idxs {
				mark(i)
			}
		}
	}

	// 批内创建的名字与批内其它名字或既有名字构成祖先分段前缀冲突。
	for i, in := range ins {
		if in.Old != "" { // 只看创建
			continue
		}
		for j, other := range ins {
			if i != j && namesConflict(in.Ref, other.Ref) {
				mark(i)
				mark(j)
			}
		}
		for existing := range s.refs {
			if namesConflict(in.Ref, existing) {
				mark(i)
			}
		}
	}
}
