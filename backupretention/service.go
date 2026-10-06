package backupretention

import (
	"fmt"
	"io"
	"sync"
)

// Option 配置 Service。
type Option func(*Service)

// WithLogger 输出每条操作的输入、输出与判定依据日志。
func WithLogger(w io.Writer) Option {
	return func(s *Service) { s.logWriter = w }
}

// Service 是分层备份保留管理服务的并发安全入口。
type Service struct {
	mu        sync.Mutex
	reg       *registry
	policy    Policy
	lastClock int64 // 上一次被接受操作的时刻；-1 表示尚无操作
	logWriter io.Writer
}

// NewService 创建服务：初始策略三层均为 0，尚无时钟基线。
func NewService(opts ...Option) *Service {
	s := &Service{reg: newRegistry(), lastClock: -1}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// checkClock 验证时钟单调性；返回错误时调用方必须原样返回且不修改任何状态。
func (s *Service) checkClock(op string, now int64) error {
	if now < 0 || now > MaxTimestamp {
		return newError(KindInvalidParam, op, "timestamp out of range")
	}
	if now < s.lastClock {
		return newError(KindClockSkew, op,
			fmt.Sprintf("clock moved backwards: now=%d last=%d", now, s.lastClock))
	}
	return nil
}

// validateBackupParams 校验备份登记的通用参数：非负整数取值域与非空标识。
func validateBackupParams(id string, size, createdAt int64) error {
	if id == "" {
		return newError(KindInvalidParam, "register", "backup id must be non-empty")
	}
	if size < 0 || size > MaxBackupSize {
		return newError(KindInvalidParam, "register", "size out of range")
	}
	if createdAt < 0 || createdAt > MaxTimestamp {
		return newError(KindInvalidParam, "register", "createdAt out of range")
	}
	return nil
}

// RegisterFull 登记一个全量备份。登记时刻即创建时刻。
//
// 错误次序：参数非法 → 时钟回退 → 标识重复。
func (s *Service) RegisterFull(id string, size, createdAt int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "RegisterFull"
	defer func() {
		if err != nil {
			s.logf("%s IN {id=%q size=%d createdAt=%d} -> ERROR kind=%d (%v)",
				op, id, size, createdAt, ErrorKindOf(err), err)
		}
	}()

	if err = validateBackupParams(id, size, createdAt); err != nil {
		return err
	}
	if err = s.checkClock(op, createdAt); err != nil {
		return err
	}
	if _, dup := s.reg.get(id); dup {
		err = newError(KindDuplicateID, op, "backup id already registered: "+id)
		return err
	}
	s.reg.add(&Backup{ID: id, Kind: KindFull, Size: size, CreatedAt: createdAt})
	s.lastClock = createdAt
	s.logf("%s IN {id=%q kind=full size=%d createdAt=%d} -> OK registered; clock=%d",
		op, id, size, createdAt, s.lastClock)
	return nil
}

// RegisterIncremental 登记一个增量备份，父备份必须已登记且创建时刻不晚于子备份。
//
// 错误次序：参数非法 → 时钟回退 → 标识重复 → 父备份不存在 → 时序矛盾。
func (s *Service) RegisterIncremental(id string, size, createdAt int64, parentID string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "RegisterIncremental"
	defer func() {
		if err != nil {
			s.logf("%s IN {id=%q size=%d createdAt=%d parent=%q} -> ERROR kind=%d (%v)",
				op, id, size, createdAt, parentID, ErrorKindOf(err), err)
		}
	}()

	if err = validateBackupParams(id, size, createdAt); err != nil {
		return err
	}
	if parentID == "" {
		err = newError(KindInvalidParam, op, "incremental backup requires a parent id")
		return err
	}
	if err = s.checkClock(op, createdAt); err != nil {
		return err
	}
	if _, dup := s.reg.get(id); dup {
		err = newError(KindDuplicateID, op, "backup id already registered: "+id)
		return err
	}
	parent, ok := s.reg.get(parentID)
	if !ok {
		err = newError(KindParentNotFound, op, "parent backup not registered: "+parentID)
		return err
	}
	if createdAt < parent.CreatedAt {
		err = newError(KindTimeContradiction, op,
			fmt.Sprintf("child createdAt %d is earlier than parent %d", createdAt, parent.CreatedAt))
		return err
	}
	s.reg.add(&Backup{
		ID: id, Kind: KindIncremental, ParentID: parentID,
		Size: size, CreatedAt: createdAt,
	})
	s.lastClock = createdAt
	s.logf("%s IN {id=%q kind=incr size=%d createdAt=%d parent=%q} -> OK registered; clock=%d",
		op, id, size, createdAt, parentID, s.lastClock)
	return nil
}

// MarkCorrupt 将备份标记为损坏；标记不可撤销，重复标记不报错。
//
// 错误次序：参数非法 → 时钟回退 → 备份不存在。
func (s *Service) MarkCorrupt(id string, now int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "MarkCorrupt"
	defer func() {
		if err != nil {
			s.logf("%s IN {id=%q now=%d} -> ERROR kind=%d (%v)",
				op, id, now, ErrorKindOf(err), err)
		}
	}()

	if id == "" {
		err = newError(KindInvalidParam, op, "backup id must be non-empty")
		return err
	}
	if err = s.checkClock(op, now); err != nil {
		return err
	}
	b, ok := s.reg.get(id)
	if !ok {
		err = newError(KindBackupNotFound, op, "backup not registered: "+id)
		return err
	}
	already := b.Corrupt
	b.Corrupt = true
	s.lastClock = now
	s.logf("%s IN {id=%q now=%d} -> OK corrupt=true (already=%t); affects recoverability of descendants",
		op, id, now, already)
	return nil
}

// SetLegalHold 设置或解除备份的法律保留；解除后重新按策略判定。
//
// 错误次序：参数非法 → 时钟回退 → 备份不存在。
func (s *Service) SetLegalHold(id string, now int64, on bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "SetLegalHold"
	defer func() {
		if err != nil {
			s.logf("%s IN {id=%q now=%d on=%t} -> ERROR kind=%d (%v)",
				op, id, now, on, ErrorKindOf(err), err)
		}
	}()

	if id == "" {
		err = newError(KindInvalidParam, op, "backup id must be non-empty")
		return err
	}
	if err = s.checkClock(op, now); err != nil {
		return err
	}
	b, ok := s.reg.get(id)
	if !ok {
		err = newError(KindBackupNotFound, op, "backup not registered: "+id)
		return err
	}
	b.LegalHold = on
	s.lastClock = now
	s.logf("%s IN {id=%q now=%d on=%t} -> OK legalHold=%t (corrupt=%t is irrelevant to legal hold)",
		op, id, now, on, b.LegalHold, b.Corrupt)
	return nil
}

// SetPolicy 变更保留策略；只影响之后的计划，不删除任何备份。
//
// 错误次序：时刻参数非法 → 时钟回退 → 层数量越界。
func (s *Service) SetPolicy(p Policy, now int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "SetPolicy"
	defer func() {
		if err != nil {
			s.logf("%s IN {policy=%+v now=%d} -> ERROR kind=%d (%v)",
				op, p, now, ErrorKindOf(err), err)
		}
	}()

	if err = s.checkClock(op, now); err != nil {
		return err
	}
	if err = p.validate(); err != nil {
		return err
	}
	old := s.policy
	s.policy = p
	s.lastClock = now
	s.logf("%s IN {daily=%d weekly=%d monthly=%d now=%d} -> OK old={%d,%d,%d}; affects only future plans",
		op, p.Daily, p.Weekly, p.Monthly, now, old.Daily, old.Weekly, old.Monthly)
	return nil
}

// Plan 只读生成当前时刻的清理计划，不改变任何状态（时钟基线除外）。
//
// 错误次序：参数非法（时刻取值域）→ 时钟回退。
func (s *Service) Plan(now int64) (plan *Plan, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "Plan"
	defer func() {
		if err != nil {
			s.logf("%s IN {now=%d} -> ERROR kind=%d (%v)", op, now, ErrorKindOf(err), err)
		}
	}()

	if err = s.checkClock(op, now); err != nil {
		return nil, err
	}
	plan = computePlan(s.reg, s.policy, now)
	s.lastClock = now
	s.logPlan(op, now, plan)
	return plan, nil
}

// Cleanup 执行清理：按计划删除全部可删除备份，返回执行所依据的计划。
//
// 删除只作用于计划中的可删除集合；依赖保护闭包保证该集合对“后代”封闭，
// 因此删除后不存在父备份缺失的备份。删除与登记互斥；删除后标识可被重新登记。
func (s *Service) Cleanup(now int64) (plan *Plan, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "Cleanup"
	defer func() {
		if err != nil {
			s.logf("%s IN {now=%d} -> ERROR kind=%d (%v)", op, now, ErrorKindOf(err), err)
		}
	}()

	if err = s.checkClock(op, now); err != nil {
		return nil, err
	}
	plan = computePlan(s.reg, s.policy, now)
	for _, pb := range plan.Deletable {
		s.reg.delete(pb.ID)
	}
	s.lastClock = now
	s.logPlan(op, now, plan)
	s.logf("%s deleted %d backups; remaining=%d", op, len(plan.Deletable), s.reg.len())
	return plan, nil
}

// logPlan 打印计划中每个备份的判定与原因，便于精确复现。
func (s *Service) logPlan(op string, now int64, plan *Plan) {
	for _, pb := range plan.Retained {
		s.logf("%s now=%d KEEP id=%q createdAt=%d direct=[%s] dependency=%t legal=%t",
			op, now, pb.ID, pb.CreatedAt,
			layersString(pb.Reason.DirectLayers), pb.Reason.Dependency, pb.Reason.LegalHold)
	}
	for _, pb := range plan.Deletable {
		s.logf("%s now=%d DELETE id=%q createdAt=%d (no retention reason)",
			op, now, pb.ID, pb.CreatedAt)
	}
}
