package ontology

import "sync"

// DedupService 编排去重计数器与判定日志：每一批变更（无论接受或拒绝）
// 都会产生一条日志；被拒绝的批不改变多重性与视图，也不会修改此前已
// 产生的任何日志条目，只是在末尾追加一条拒绝记录。
//
// Apply 在服务层串行化，从而“应用状态、读取批后视图、追加日志”三者
// 作为一个原子顺序发生，日志序号与状态演进顺序严格一致。
// 同一输入序列反复执行得到完全相同的输出与日志（不注入墙钟时间）。
type DedupService struct {
	mu      sync.Mutex
	counter *DedupCounter
	journal *Journal
}

// NewDedupService 创建单批条目数上限为 maxEntries 的服务。
func NewDedupService(maxEntries int) *DedupService {
	return &DedupService{
		counter: NewDedupCounter(maxEntries),
		journal: NewJournal(),
	}
}

// Apply 原子地应用一批变更并记录判定日志，返回判定结果。
func (s *DedupService) Apply(changes []Change) ApplyResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.counter.Apply(changes)
	s.journal.Append(LogEntry{
		Input:     changes,
		Accepted:  res.Accepted,
		Reason:    res.Reason,
		EntryIdx:  res.EntryIndex,
		Changes:   res.Changes,
		ViewAfter: s.counter.View(),
	})
	return res
}

// View 返回各组当前去重计数的一致快照。
func (s *DedupService) View() map[string]int {
	return s.counter.View()
}

// Multiplicity 返回指定 (组, 值) 的当前净多重性。
func (s *DedupService) Multiplicity(group, value string) int {
	return s.counter.Multiplicity(group, value)
}

// Journal 返回底层判定日志。
func (s *DedupService) Journal() *Journal {
	return s.journal
}
