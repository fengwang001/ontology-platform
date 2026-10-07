package lsm

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
)

// Service 是分层日志结构存储的压实任务选择与安装服务。
// 所有公开方法都可并发调用，内部以互斥锁串行化，
// 因此任何并发执行都等价于某个串行顺序；相同的操作序列重放
// 会得到完全相同的计划（决策路径不依赖 map 迭代序与浮点运算）。
type Service struct {
	mu     sync.Mutex
	cfg    Config
	levels []levelState

	files  map[uint64]FileMeta // 全部已登记文件
	pinned map[uint64]uint64   // 被占用文件 -> 占用它的计划编号
	plans  map[uint64]*Plan    // 在途计划
	nextID uint64              // 下一个计划编号，从 1 开始
}

// New 创建服务；配置非法时返回 ErrInvalidArgument。
func New(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	levels := make([]levelState, cfg.NumLevels)
	for l := range levels {
		levels[l] = newLevelState(l == 0)
	}
	return &Service{
		cfg:    cfg,
		levels: levels,
		files:  make(map[uint64]FileMeta),
		pinned: make(map[uint64]uint64),
		plans:  make(map[uint64]*Plan),
		nextID: 1,
	}, nil
}

// AddFile 登记一个文件到其所在层。
// 非零层要求新文件与层内已有文件满足层不变量（区间不重叠，允许端点相接）。
func (s *Service) AddFile(f FileMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := f.validate(s.cfg.NumLevels); err != nil {
		return err
	}
	if _, dup := s.files[f.ID]; dup {
		return fmt.Errorf("%w: file %d already registered", ErrInvalidArgument, f.ID)
	}
	if err := s.levels[f.Level].checkInvariant(f); err != nil {
		return err
	}
	s.levels[f.Level].add(f)
	s.files[f.ID] = f
	return nil
}

// File 查询已登记文件；不存在时返回 ErrFileNotFound。
func (s *Service) File(id uint64) (FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	if !ok {
		return FileMeta{}, fmt.Errorf("%w: file %d", ErrFileNotFound, id)
	}
	return f, nil
}

// Files 返回某层全部文件：零层按编号升序，非零层按 (Smallest, Largest, ID) 升序。
func (s *Service) Files(level int) ([]FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if level < 0 || level >= s.cfg.NumLevels {
		return nil, fmt.Errorf("%w: level %d out of range", ErrInvalidArgument, level)
	}
	return s.levels[level].files(), nil
}

// Pinned 报告文件当前是否被某个在途计划占用。
func (s *Service) Pinned(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isPinned(id)
}

// Pick 计算本次压实计划。
// 返回 (nil, nil) 表示没有任何层达标或所有达标层都因在途冲突而放弃，
// 计划为空是正常结果而非错误。计划一旦返回，其全部输入立即被占用。
func (s *Service) Pick() (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pickLocked()
}

// adoptPlan 登记计划并立即占用其全部输入。
func (s *Service) adoptPlan(p *Plan) {
	p.ID = s.nextID
	s.nextID++
	s.plans[p.ID] = p
	for _, f := range p.allInputs() {
		s.pinned[f.ID] = p.ID
	}
}

// isPinned 报告文件是否被在途计划占用。
func (s *Service) isPinned(id uint64) bool {
	_, ok := s.pinned[id]
	return ok
}

// Install 安装压实结果：outputs 由调用方给出，是压实产生的新文件，
// 必须全部落在计划的目标层，且互相之间、以及与目标层所有未被消耗的
// 文件之间满足层不变量。对于直接下移计划，outputs 也可以是输入文件
// 本身（同一编号）改挂到目标层。
//
// 成功则原子地删除输入、加入输出、释放占用，并把源层的上次压实终点键
// 更新为本次源层输入的最大键；失败则整体不生效且占用保持，
// 调用方可修正后再次安装或取消。
//
// 同时触及多类错误时只报告次序最靠前的一类：
// 参数非法 > 文件不存在 > 文件已被占用 > 计划不存在 > 层不变量被破坏。
func (s *Service) Install(planID uint64, outputs []FileMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	plan, planOK := s.plans[planID]

	// 1. 参数非法：输出元数据自身合法、编号互不重复、落在目标层。
	var argErr error
	seen := make(map[uint64]bool, len(outputs))
	for _, o := range outputs {
		if err := o.validate(s.cfg.NumLevels); err != nil {
			argErr = firstError(argErr, err)
			continue
		}
		if seen[o.ID] {
			argErr = firstError(argErr, fmt.Errorf("%w: duplicate output file id %d", ErrInvalidArgument, o.ID))
		}
		seen[o.ID] = true
		if planOK && o.Level != plan.TargetLevel {
			argErr = firstError(argErr, fmt.Errorf("%w: output file %d in level %d, want target level %d",
				ErrInvalidArgument, o.ID, o.Level, plan.TargetLevel))
		}
	}
	if argErr != nil {
		return argErr
	}

	// 2. 文件已被占用 / 编号冲突：输出若复用已登记编号（直接下移的情形），
	//    该编号必须是本计划的输入；与被占用文件冲突报“文件已被占用”，
	//    与普通文件冲突报“参数非法”。
	for _, o := range outputs {
		if _, exists := s.files[o.ID]; !exists {
			continue // 全新文件，正常
		}
		if planOK && plan.containsInput(o.ID) {
			continue // 直接下移：输入文件改挂到目标层
		}
		if s.isPinned(o.ID) {
			return fmt.Errorf("%w: output file %d collides with pinned file", ErrFilePinned, o.ID)
		}
		return fmt.Errorf("%w: output file %d collides with existing file that is not an input of plan %d",
			ErrInvalidArgument, o.ID, planID)
	}

	// 3. 计划不存在。
	if !planOK {
		return fmt.Errorf("%w: plan %d", ErrPlanNotFound, planID)
	}

	// 4. 层不变量：输出互相之间、以及与目标层未被消耗的文件之间，
	//    区间不得出现非端点相接的重叠。
	if err := s.checkOutputsInvariant(plan, outputs); err != nil {
		return err
	}

	// 原子生效：删除输入、加入输出、释放占用、更新起点记录。
	for _, f := range plan.allInputs() {
		s.levels[f.Level].remove(f)
		delete(s.files, f.ID)
		delete(s.pinned, f.ID)
	}
	for _, o := range outputs {
		s.levels[o.Level].add(o)
		s.files[o.ID] = o
	}
	if plan.Level > 0 {
		in := inputInterval(plan.Inputs)
		lv := &s.levels[plan.Level]
		lv.lastKey = append(lv.lastKey[:0], in.hi...)
		lv.hasLastKey = true
	}
	delete(s.plans, planID)
	return nil
}

// checkOutputsInvariant 校验输出集合在目标层内满足层不变量。
func (s *Service) checkOutputsInvariant(plan *Plan, outputs []FileMeta) error {
	target := plan.TargetLevel
	consumed := make(map[uint64]bool, len(plan.NextInputs))
	for _, f := range plan.NextInputs {
		consumed[f.ID] = true
	}
	// 目标层未被消耗的文件 + 全部输出，一起排序后检查相邻对。
	var all []FileMeta
	for _, f := range s.levels[target].ix.files {
		if !consumed[f.ID] {
			all = append(all, f)
		}
	}
	for _, o := range outputs {
		// 若输出是输入文件复用（直接下移），它当前不在目标层，直接加入即可。
		all = append(all, o)
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if prev.ID == cur.ID {
			continue
		}
		if bytes.Compare(prev.Largest, cur.Smallest) > 0 {
			return fmt.Errorf("%w: file %d [%x,%x] overlaps file %d [%x,%x] in level %d",
				ErrInvariant, prev.ID, prev.Smallest, prev.Largest, cur.ID, cur.Smallest, cur.Largest, target)
		}
	}
	return nil
}

// Cancel 取消一个在途计划：释放其全部占用，不更新任何起点记录。
func (s *Service) Cancel(planID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return fmt.Errorf("%w: plan %d", ErrPlanNotFound, planID)
	}
	for _, f := range plan.allInputs() {
		delete(s.pinned, f.ID)
	}
	delete(s.plans, planID)
	return nil
}
