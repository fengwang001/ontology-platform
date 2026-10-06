package backup

import "sync"

// Service 是分层备份保留管理服务。所有方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu       sync.Mutex
	recs     map[string]*rec
	order    []string // 存活备份的登记顺序（父先于子，创建时刻非递减）
	policy   Policy
	lastTime int64 // 上一次被接受操作的时刻
}

// NewService 创建一个空服务，默认策略为三层均不保留。
func NewService() *Service {
	return &Service{recs: make(map[string]*rec)}
}

// Info 是备份的只读快照，用于检视。
type Info struct {
	ID        string
	Kind      Kind
	Parent    string
	Size      int64
	CreatedAt int64
	Corrupted bool
	LegalHold bool
}

// GetBackup 返回一个已登记备份的快照；不存在时 ok 为 false。
func (s *Service) GetBackup(id string) (info Info, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[id]
	if !ok {
		return Info{}, false
	}
	info = Info{
		ID:        r.id,
		Kind:      Incremental,
		Parent:    r.parent,
		Size:      r.size,
		CreatedAt: r.createdAt,
		Corrupted: r.corrupted,
		LegalHold: r.held,
	}
	if r.parent == "" {
		info.Kind = Full
	}
	return info, true
}

// RegisterBackup 登记一个备份，登记时刻 now 即该备份的创建时刻。
// 开销与已登记备份总数无关（哈希表插入 + 追加）。
func (s *Service) RegisterBackup(now int64, id string, kind Kind, parent string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 1. 参数非法
	if id == "" {
		return &Error{ErrInvalidArgument, "备份标识不能为空"}
	}
	if kind != Full && kind != Incremental {
		return &Error{ErrInvalidArgument, "未知的备份类型"}
	}
	if kind == Full && parent != "" {
		return &Error{ErrInvalidArgument, "全量备份不能有父备份"}
	}
	if kind == Incremental && parent == "" {
		return &Error{ErrInvalidArgument, "增量备份必须指定父备份"}
	}
	if size < 0 || size > MaxSize {
		return &Error{ErrInvalidArgument, "备份大小越界"}
	}
	if err := s.checkTime(now); err != nil {
		return err
	}
	// 3. 标识重复
	if _, ok := s.recs[id]; ok {
		return &Error{ErrDuplicateID, "备份标识已登记: " + id}
	}
	// 4. 父备份不存在
	var pr *rec
	if kind == Incremental {
		pr = s.recs[parent]
		if pr == nil {
			return &Error{ErrParentNotFound, "父备份不存在: " + parent}
		}
	}
	// 5. 时序矛盾：子早于父（取等合法）
	if pr != nil && now < pr.createdAt {
		return &Error{ErrTimeOrder, "子备份创建时刻早于父备份"}
	}
	s.recs[id] = &rec{id: id, parent: parent, size: size, createdAt: now}
	s.order = append(s.order, id)
	s.lastTime = now
	return nil
}

// MarkCorrupted 标记备份损坏。损坏不可撤销，重复标记不是错误。
func (s *Service) MarkCorrupted(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.lookup(now, id)
	if err != nil {
		return err
	}
	r.corrupted = true
	s.lastTime = now
	return nil
}

// SetLegalHold 对备份设置法律保留；对损坏备份也可设置。
func (s *Service) SetLegalHold(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.lookup(now, id)
	if err != nil {
		return err
	}
	r.held = true
	s.lastTime = now
	return nil
}

// ReleaseLegalHold 解除法律保留；解除后重新按策略判定。
// 对未设置法律保留的备份调用是空操作，不是错误。
func (s *Service) ReleaseLegalHold(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.lookup(now, id)
	if err != nil {
		return err
	}
	r.held = false
	s.lastTime = now
	return nil
}

// SetPolicy 变更保留策略，只影响之后的计划，本身不删除任何备份。
func (s *Service) SetPolicy(now int64, p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTime(now); err != nil {
		return err
	}
	// 层数量越界在错误优先级中位于时钟回退之后。
	if !validCount(p.Daily) || !validCount(p.Weekly) || !validCount(p.Monthly) {
		return &Error{ErrRetentionLimit, "层保留数量须在 [0, 1000] 之间"}
	}
	s.policy = p
	s.lastTime = now
	return nil
}

// PlanCleanup 生成只读清理计划，不改变任何备份状态。
// 作为被接受的带时刻操作，它会推进单调时钟。
func (s *Service) PlanCleanup(now int64) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTime(now); err != nil {
		return Plan{}, err
	}
	plan := computePlan(s.recs, s.order, s.policy, now, nil)
	s.lastTime = now
	return plan, nil
}

// ExecuteCleanup 生成当前计划并真正删除其中的可删除备份，返回执行的计划。
// 连续执行两次，第二次不删除任何备份。
func (s *Service) ExecuteCleanup(now int64) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTime(now); err != nil {
		return Plan{}, err
	}
	plan := computePlan(s.recs, s.order, s.policy, now, nil)
	if len(plan.Deletable) > 0 {
		deleted := make(map[string]struct{}, len(plan.Deletable))
		for _, id := range plan.Deletable {
			delete(s.recs, id)
			deleted[id] = struct{}{}
		}
		// 依赖保护保证被保留备份的祖先也被保留，
		// 因此删除后不会留下父备份已不存在的备份。
		kept := make([]string, 0, len(s.recs))
		for _, id := range s.order {
			if _, gone := deleted[id]; !gone {
				kept = append(kept, id)
			}
		}
		s.order = kept
	}
	s.lastTime = now
	return plan, nil
}

// checkTime 校验时刻：先参数非法（越界），后时钟回退。
func (s *Service) checkTime(now int64) error {
	if now < 0 || now > MaxTime {
		return &Error{ErrInvalidArgument, "时刻越界"}
	}
	if now < s.lastTime {
		return &Error{ErrClockRollback, "时刻早于上一次被接受操作的时刻"}
	}
	return nil
}

// lookup 按错误优先级校验公共部分（参数非法、时钟回退、备份不存在）。
func (s *Service) lookup(now int64, id string) (*rec, error) {
	if id == "" {
		return nil, &Error{ErrInvalidArgument, "备份标识不能为空"}
	}
	if err := s.checkTime(now); err != nil {
		return nil, err
	}
	r := s.recs[id]
	if r == nil {
		return nil, &Error{ErrBackupNotFound, "备份不存在: " + id}
	}
	return r, nil
}

func validCount(n int) bool { return n >= 0 && n <= MaxRetentionCount }
