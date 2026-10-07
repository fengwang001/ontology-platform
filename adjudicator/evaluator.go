package adjudicator

import "slices"

// Index 为一批备份快照的只读索引：一次构建（O(总规模)），
// 可重复用于任意多次单条记录判定。构建后不再读取快照，
// 因此多次查询的摊销开销只与各自的依赖链长度相关。
type Index struct {
	scopes  [categoryCount]scope
	records map[RecordRef]*Record
}

// BuildIndex 为快照构建只读索引。不会修改快照。
func BuildIndex(s *Snapshot) *Index {
	idx := &Index{records: make(map[RecordRef]*Record)}
	var a assessor
	for c := 0; c < categoryCount; c++ {
		idx.scopes[c] = a.assess(s.Backups[c])
		for i := range s.Backups[c].Records {
			r := s.Backups[c].Records[i]
			idx.records[r.Ref] = &r
		}
	}
	return idx
}

// evaluator 负责跨类别依赖关系的核对：
// 在 assessor 给出的各类别可用范围之上，沿依赖链判定每条记录是否可重建。
// 判定开销通过记忆化与边访问计数保证只与记录自身依赖链长度相关。
type evaluator struct {
	idx *Index
	// edgeVisits 统计判定过程中访问的依赖边条数，用于开销可复核。
	edgeVisits int64
}

// newEvaluator 基于只读索引构建核对器。
func newEvaluator(idx *Index) *evaluator {
	return &evaluator{idx: idx}
}

// evalResult 为一次依赖核对的结果。
type evalResult struct {
	// rebuildable 记录每条被核对记录是否可重建。
	rebuildable map[RecordRef]bool
	// blockedBy 记录导致不可重建的直接原因记录。
	blockedBy map[RecordRef][]RecordRef
	// cyclic 标记处于循环依赖分量中、除此之外本可重建的记录。
	cyclic map[RecordRef]bool
}

// selfOK 判定记录自身在所属类别备份中是否可恢复（不含依赖）。
func (e *evaluator) selfOK(ref RecordRef) bool {
	sc := e.idx.scopes[int(ref.Category)]
	return sc.available && sc.recoverable[ref.ID]
}

// evaluate 核对从 roots 可达的全部记录；roots 为 nil 时核对全部记录。
//
// 实现为对依赖图求强连通分量（Tarjan），把每个分量当作一个整体判定：
// 分量可重建当且仅当分量内每条记录自身可恢复、没有指向备份中
// 不存在的记录的依赖、且其依赖的分量全部可重建。
// Tarjan 按汇优先的顺序产出分量，恰好保证依赖分量先于被依赖分量处理。
// 只访问从 roots 可达的节点与边，因此单条记录的核对开销只随其
// 依赖链长度增长，与备份总规模无关。
func (e *evaluator) evaluate(roots []RecordRef) evalResult {
	t := &tarjan{
		e:       e,
		index:   map[RecordRef]int{},
		lowlink: map[RecordRef]int{},
		onStack: map[RecordRef]bool{},
		compOf:  map[RecordRef]int{},
	}
	if roots == nil {
		roots = make([]RecordRef, 0, len(e.idx.records))
		for ref := range e.idx.records {
			roots = append(roots, ref)
		}
		slices.SortFunc(roots, compareRefs)
	}
	for _, ref := range roots {
		if _, ok := e.idx.records[ref]; !ok {
			continue
		}
		if _, seen := t.index[ref]; !seen {
			t.strongConnect(ref)
		}
	}

	res := evalResult{
		rebuildable: map[RecordRef]bool{},
		blockedBy:   map[RecordRef][]RecordRef{},
		cyclic:      map[RecordRef]bool{},
	}
	compOK := make([]bool, len(t.comps))
	for ci, comp := range t.comps {
		ok := true
		var causes []RecordRef
		for _, ref := range comp {
			if !e.selfOK(ref) {
				ok = false
				causes = append(causes, ref)
			}
			for _, dep := range e.idx.records[ref].DependsOn {
				e.edgeVisits++
				depRec, exists := e.idx.records[dep]
				if !exists {
					// 依赖的记录在备份中不存在（典型情形：所属类别
					// 整体缺失或损坏），直接判定不可重建。
					ok = false
					causes = append(causes, dep)
					continue
				}
				depComp := t.compOf[depRec.Ref]
				if depComp != ci && !compOK[depComp] {
					ok = false
					causes = append(causes, dep)
				}
			}
		}
		compOK[ci] = ok
		cyclic := len(comp) > 1 || hasSelfLoop(e.idx.records[comp[0]])
		for _, ref := range comp {
			switch {
			case ok && cyclic:
				// 分量内部循环：除此之外本可重建，但顺序无法确定。
				res.cyclic[ref] = true
			case ok:
				res.rebuildable[ref] = true
			default:
				res.rebuildable[ref] = false
				res.blockedBy[ref] = dedupeRefs(causes)
			}
		}
	}
	return res
}

// EdgeVisits 返回判定过程累计访问的依赖边条数。
func (e *evaluator) EdgeVisits() int64 {
	return e.edgeVisits
}

// hasSelfLoop 报告记录是否直接依赖自身。
func hasSelfLoop(r *Record) bool {
	for _, dep := range r.DependsOn {
		if dep == r.Ref {
			return true
		}
	}
	return false
}

// compareRefs 为记录引用的固定全序：先类别优先级，再 ID 字典序。
func compareRefs(a, b RecordRef) int {
	if a.Category != b.Category {
		return int(a.Category) - int(b.Category)
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

// dedupeRefs 去重并排序，保证输出确定。
func dedupeRefs(refs []RecordRef) []RecordRef {
	slices.SortFunc(refs, compareRefs)
	return slices.Compact(refs)
}

// tarjan 为 Tarjan 强连通分量算法的状态。
type tarjan struct {
	e       *evaluator
	index   map[RecordRef]int
	lowlink map[RecordRef]int
	onStack map[RecordRef]bool
	stack   []RecordRef
	next    int
	compOf  map[RecordRef]int
	comps   [][]RecordRef
}

// strongConnect 为 Tarjan 算法的递归主体。
// 依赖边只指向备份中存在的记录；不存在的依赖在分量评估阶段处理。
func (t *tarjan) strongConnect(ref RecordRef) {
	t.index[ref] = t.next
	t.lowlink[ref] = t.next
	t.next++
	t.stack = append(t.stack, ref)
	t.onStack[ref] = true

	for _, dep := range t.e.idx.records[ref].DependsOn {
		if _, exists := t.e.idx.records[dep]; !exists {
			continue
		}
		if _, seen := t.index[dep]; !seen {
			t.strongConnect(dep)
			t.lowlink[ref] = min(t.lowlink[ref], t.lowlink[dep])
		} else if t.onStack[dep] {
			t.lowlink[ref] = min(t.lowlink[ref], t.index[dep])
		}
	}

	if t.lowlink[ref] == t.index[ref] {
		var comp []RecordRef
		for {
			top := t.stack[len(t.stack)-1]
			t.stack = t.stack[:len(t.stack)-1]
			t.onStack[top] = false
			comp = append(comp, top)
			if top == ref {
				break
			}
		}
		slices.SortFunc(comp, compareRefs)
		ci := len(t.comps)
		for _, member := range comp {
			t.compOf[member] = ci
		}
		t.comps = append(t.comps, comp)
	}
}
