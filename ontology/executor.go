package ontology

import (
	"context"
	"sort"
	"sync"
)

// SyncHooks 把每次“尝试”的调度权交给外部（脚本化调度器或真实并发）。
// WaitTurn 在一次尝试的读+CAS 临界区之前被调用并阻塞，直到外部放行；
// DoneAttempt 在该尝试判定落定（含日志记录）之后被调用，恰好一次。
type SyncHooks interface {
	// BeforeRead 在一次尝试读取基线之前调用并阻塞，直到调度器放行。
	// 放行后执行器持锁读取一致快照并释放锁。
	BeforeRead(opID, attempt int)
	// BeforeCommit 在读完基线、执行 CAS 之前调用并阻塞。
	// 脚本连续放行多个 BeforeRead 即可让多个动作读到同一基线；
	// 再按期望生效顺序放行 BeforeCommit。
	BeforeCommit(opID, attempt int, base Baseline)
	// AfterCommit 在 CAS 落定（含日志）后调用，恰好一次。
	AfterCommit(rec AttemptRecord)
}

// Journal 记录运行中的全部判定轨迹，供重放与对照。
type Journal struct {
	mu       sync.Mutex
	attempts []AttemptRecord
}

// Executor 持有按名管理的实例集合并执行动作的乐观重试循环。
type Executor struct {
	mu        sync.Mutex
	instances map[string]*Instance
	hooks     SyncHooks
	journal   *Journal
}

// NewExecutor 创建一个执行器。hooks 为 nil 时不同步等待，
// 适合真实并发与 -race 检测；确定性测试可注入脚本化调度器。
func NewExecutor(hooks SyncHooks) *Executor {
	return &Executor{
		instances: make(map[string]*Instance),
		hooks:     hooks,
		journal:   &Journal{},
	}
}

func (j *Journal) append(rec AttemptRecord) {
	j.mu.Lock()
	j.attempts = append(j.attempts, rec)
	j.mu.Unlock()
}

func (j *Journal) snapshot() []AttemptRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]AttemptRecord, len(j.attempts))
	copy(out, j.attempts)
	return out
}

func (e *Executor) beforeRead(opID, attempt int) {
	if e.hooks != nil {
		e.hooks.BeforeRead(opID, attempt)
	}
}

func (e *Executor) beforeCommit(opID, attempt int, base Baseline) {
	if e.hooks != nil {
		e.hooks.BeforeCommit(opID, attempt, base)
	}
}

func (e *Executor) afterCommit(rec AttemptRecord) {
	if e.hooks != nil {
		e.hooks.AfterCommit(rec)
	}
}

// Run 按 op 描述执行完整的乐观重试循环并返回终结结果。
//
// 每次尝试：读取基线 ->（同步点）-> 计算变换并 CAS 判定 ->（同步点）。
// 终结判定顺序保证为：成功提交 > 被更高权限立即抢占 > 重试预算耗尽。
// 抢占立即发生且不消耗预算：即使冲突尝试本应耗尽最后一次预算，
// 只要抢占条件成立，仍判定为 StatusPreempted。
func (e *Executor) Run(ctx context.Context, op Op) OpResult {
	result := OpResult{OpID: op.ID, Instance: op.Instance, Priv: op.Priv}
	target := e.instance(op.Instance)
	if op.MaxAttempts < 1 {
		op.MaxAttempts = 1
	}

	for attempt := 1; attempt <= op.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			break
		}

		// 阶段 0：等待调度器放行读。
		e.beforeRead(op.ID, attempt)

		// 阶段一：持锁读取一致快照，随后释放锁并停在提交闸门。
		// 多个动作可连续读完并持有同一基线、同时在途。
		target.lock()
		base := target.snapshot()
		next := op.Apply(copyAttrs(target.attrs))
		target.unlock()

		e.beforeCommit(op.ID, attempt, base)

		// 阶段二（CAS）：被调度器选中后重新持锁，以版本/高水位
		// 判定基线是否已被他人推进。
		target.lock()
		v, fresh := target.tryCommit(op.Priv, base, next)
		sawVersion, sawHighWater := fresh.Version, fresh.HighWater
		committedVersion := int64(0)
		if v == verdictCommitted {
			committedVersion = sawVersion
		}

		terminal := false
		switch v {
		case verdictCommitted:
			terminal = true
			result.Status = StatusCommitted
			result.CommittedAt = committedVersion
		case verdictPreempted:
			terminal = true
			result.Status = StatusPreempted
		case verdictConflict:
			if attempt == op.MaxAttempts {
				terminal = true
				result.Status = StatusExhausted
			}
		}

		rec := AttemptRecord{
			OpID:          op.ID,
			Attempt:       attempt,
			Priv:          op.Priv,
			BaseVersion:   base.Version,
			BaseHighWater: base.HighWater,
			SawVersion:    sawVersion,
			SawHighWater:  sawHighWater,
			Verdict:       verdictName(v),
			CommittedVer:  committedVersion,
		}
		e.journal.append(rec)
		result.Attempts = append(result.Attempts, rec)

		// 判定同步点在解锁后触发（不持锁，不影响后续尝试）。
		target.unlock()
		e.afterCommit(rec)
		if terminal {
			return result
		}
	}

	result.Status = StatusExhausted
	return result
}

func verdictName(v verdict) string {
	switch v {
	case verdictCommitted:
		return "committed"
	case verdictPreempted:
		return "preempted"
	case verdictConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

func copyAttrs(in Attrs) Attrs {
	out := make(Attrs, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (e *Executor) instance(name string) *Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	target, ok := e.instances[name]
	if !ok {
		target = newInstance(name)
		e.instances[name] = target
	}
	return target
}

func (e *Executor) FinalState(name string) (int64, Privilege, Attrs) {
	return e.instance(name).readState()
}

// Attempts 返回按 OpID、Attempt 稳定排序的全部尝试轨迹。
func (e *Executor) Attempts() []AttemptRecord {
	out := e.journal.snapshot()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OpID != out[j].OpID {
			return out[i].OpID < out[j].OpID
		}
		return out[i].Attempt < out[j].Attempt
	})
	return out
}

// RawAttempts 返回真实 append 顺序（即交织顺序）的尝试轨迹。
func (e *Executor) RawAttempts() []AttemptRecord {
	return e.journal.snapshot()
}
