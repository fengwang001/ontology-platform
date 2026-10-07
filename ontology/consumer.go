package ontology

import (
	"sync"
	"time"
)

// Outcome 是消费一条事件的最终结果的分类。
type Outcome int

const (
	// OutcomeCompensated 补偿动作执行完成（含故障后的续作完成）。
	OutcomeCompensated Outcome = iota
	// OutcomeDuplicateSkipped 重复投递且补偿已达终态，跳过。
	OutcomeDuplicateSkipped
	// OutcomeAbandoned 原始动作在补偿开始前被撤销，补偿被放弃。
	OutcomeAbandoned
	// OutcomeNoAction 无需动作：未注册补偿的动作类型、补偿已达终态、
	// 或撤销到达时补偿已开始（按规则继续完成，撤销本身不改变状态）。
	OutcomeNoAction
	// OutcomeError 发生已分类错误，停在确定的副作用边界上。
	OutcomeError
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCompensated:
		return "Compensated"
	case OutcomeDuplicateSkipped:
		return "DuplicateSkipped"
	case OutcomeAbandoned:
		return "Abandoned"
	case OutcomeNoAction:
		return "NoAction"
	case OutcomeError:
		return "Error"
	default:
		return "Unknown"
	}
}

// ConsumeResult 是消费一条事件的结果。
type ConsumeResult struct {
	Outcome Outcome
	// Record 是补偿记录的最终快照（无记录时为零值）。
	Record CompensationRecord
	Err    *ConsumeError
}

// Consumer 是变更流消费端。所有持久化状态（去重索引、补偿日志、对象
// 存储）都在 Storage 中，Consumer 本身无状态：进程故障重启后用同一
// Storage 构造新的 Consumer 即可安全续作。
//
// 可串行化保证：Consume 全程持有全局互斥锁，任意并发消费等价于按
// 锁的获得顺序串行执行。
type Consumer struct {
	mu       sync.Mutex
	dedup    *Deduper
	journal  *Journal
	store    *ObjectStore
	builders map[string]CompensationBuilder

	// crashHook 在每项副作用提交（apply+journal 落盘）之后被调用，
	// 仅用于测试注入故障。测试在钩子内 panic 即可模拟进程崩溃：
	// 已提交的副作用保持提交状态，随后用同一 Storage 构造新的
	// Consumer 续作。
	crashHook func(actionExecutionID string, committed int)
}

// Storage 聚合子系统的全部持久化状态。
type Storage struct {
	Dedup   *Deduper
	Journal *Journal
	Store   *ObjectStore
}

// NewStorage 构造一套空的持久化状态。
func NewStorage(now func() time.Time) *Storage {
	return &Storage{
		Dedup:   NewDeduper(now),
		Journal: NewJournal(),
		Store:   NewObjectStore(),
	}
}

// ConsumerOption 是 Consumer 的可选配置。
type ConsumerOption func(*Consumer)

// WithCrashHook 注入故障钩子（仅测试使用）。
func WithCrashHook(hook func(actionExecutionID string, committed int)) ConsumerOption {
	return func(c *Consumer) { c.crashHook = hook }
}

// NewConsumer 在持久化状态之上构造消费端，并注册各动作类型的
// 补偿计划构造器。
func NewConsumer(st *Storage, builders map[string]CompensationBuilder, opts ...ConsumerOption) *Consumer {
	c := &Consumer{
		dedup:    st.Dedup,
		journal:  st.Journal,
		store:    st.Store,
		builders: builders,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Consume 消费一条变更事件。整个过程在全局锁内完成，保证任意并发
// 消费序列的可观察结果等价于某个全局串行顺序。
func (c *Consumer) Consume(evt ChangeEvent) ConsumeResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	verdict, _ := c.dedup.Classify(evt)
	switch verdict {
	case VerdictUndecidable:
		return c.fail(evt, newError(ErrIdentityUndecidable, evt,
			"事件标识缺失或自相矛盾，无法判断与历史事件的关系"))
	case VerdictDuplicate:
		return c.resumeOrSkip(evt)
	default:
		if evt.Kind == KindActionReverted {
			return c.handleUndo(evt)
		}
		return c.runCompensation(evt)
	}
}

// resumeOrSkip 处理重复投递：若补偿处于未完成状态则续作，否则跳过。
func (c *Consumer) resumeOrSkip(evt ChangeEvent) ConsumeResult {
	rec, ok := c.journal.Get(evt.ActionExecutionID)
	if !ok {
		// 去重索引记得该事件，但补偿历史丢失：无法安全续作。
		return c.fail(evt, newError(ErrHistoryMissing, evt,
			"补偿历史记录缺失，无法确定已提交的副作用边界"))
	}
	switch rec.State {
	case StateInProgress:
		return c.resume(evt, rec)
	case StateFailed:
		return ConsumeResult{Outcome: OutcomeError, Record: rec, Err: rec.Err}
	default: // Completed / Abandoned / Pending（Pending 不会出现，见 runCompensation）
		return ConsumeResult{Outcome: OutcomeDuplicateSkipped, Record: rec}
	}
}

// runCompensation 处理首次到达的动作成功事件。
func (c *Consumer) runCompensation(evt ChangeEvent) ConsumeResult {
	// 撤销可能先于成功事件被消费（乱序到达），此时补偿已被放弃。
	if rec, ok := c.journal.Get(evt.ActionExecutionID); ok && rec.State == StateAbandoned {
		return ConsumeResult{Outcome: OutcomeAbandoned, Record: rec}
	}

	builder, registered := c.builders[evt.ActionType]
	if !registered {
		// 未注册补偿的动作类型：登记一条零副作用的已完成记录，
		// 使后续重复投递按正常重复处理而非误报历史缺失。
		rec := CompensationRecord{
			ActionExecutionID: evt.ActionExecutionID,
			ActionType:        evt.ActionType,
			State:             StateCompleted,
		}
		c.journal.put(rec)
		return ConsumeResult{Outcome: OutcomeNoAction, Record: rec}
	}
	effects, err := builder(evt)
	if err != nil {
		return c.failWithRecord(evt, nil, newError(ErrAtomicityViolation, evt,
			"补偿计划构造失败: %v", err))
	}

	rec := CompensationRecord{
		ActionExecutionID: evt.ActionExecutionID,
		ActionType:        evt.ActionType,
		State:             StateInProgress,
		Effects:           make([]EffectRecord, len(effects)),
	}
	for i, e := range effects {
		rec.Effects[i] = EffectRecord{Key: effectKey(evt.ActionExecutionID, i), Effect: e}
	}

	// 错误优先级：目标缺失(3) 先于 原子性校验(4)。
	for _, e := range effects {
		if !c.store.Exists(e.ObjectID) {
			return c.failWithRecord(evt, &rec, newError(ErrTargetMissing, evt,
				"补偿目标对象 %q 不存在或已被撤销", e.ObjectID))
		}
	}
	if verr := validatePlan(effects); verr != "" {
		return c.failWithRecord(evt, &rec, newError(ErrAtomicityViolation, evt, "%s", verr))
	}

	// 预写日志：先落盘完整计划，再逐项提交副作用。
	c.journal.put(rec)
	return c.execute(evt, rec)
}

// resume 在故障重启后续作部分生效的补偿：跳过已提交副作用，只施加
// 剩余未提交部分，绝不从头重放。
func (c *Consumer) resume(evt ChangeEvent, rec CompensationRecord) ConsumeResult {
	if len(rec.Effects) == 0 {
		rec.State = StateFailed
		rec.Err = newError(ErrHistoryMissing, evt,
			"补偿已部分生效但副作用明细缺失，无法确定续作边界")
		c.journal.put(rec)
		return ConsumeResult{Outcome: OutcomeError, Record: rec, Err: rec.Err}
	}
	return c.execute(evt, rec)
}

// execute 逐项提交未提交的副作用。apply 与 journal 落盘在同一临界区
// 内完成，构成原子提交点；故障注入点位于每项提交之后。
func (c *Consumer) execute(evt ChangeEvent, rec CompensationRecord) ConsumeResult {
	for i := range rec.Effects {
		if rec.Effects[i].Committed {
			continue
		}
		if !c.store.apply(rec.Effects[i].Effect) {
			rec.State = StateFailed
			rec.Err = newError(ErrTargetMissing, evt,
				"续作时目标对象 %q 已不存在", rec.Effects[i].Effect.ObjectID)
			c.journal.put(rec)
			return ConsumeResult{Outcome: OutcomeError, Record: rec, Err: rec.Err}
		}
		rec.Effects[i].Committed = true
		rec.State = StateInProgress
		c.journal.put(rec)
		if c.crashHook != nil {
			c.crashHook(rec.ActionExecutionID, rec.CommittedCount())
		}
	}
	rec.State = StateCompleted
	c.journal.put(rec)
	return ConsumeResult{Outcome: OutcomeCompensated, Record: rec}
}

// handleUndo 处理原始动作的撤销事件。规则（确定性，不依赖到达时刻）：
//   - 补偿尚未提交任何副作用（含记录不存在）→ 放弃补偿（StateAbandoned）；
//   - 补偿已提交至少一项副作用 → 补偿必须继续完成，撤销不改变其执行。
func (c *Consumer) handleUndo(evt ChangeEvent) ConsumeResult {
	origID, _ := evt.Payload["OriginalActionExecutionID"].(string)
	if origID == "" {
		return c.fail(evt, newError(ErrIdentityUndecidable, evt,
			"撤销事件缺少 OriginalActionExecutionID"))
	}
	rec, ok := c.journal.Get(origID)
	if !ok {
		rec = CompensationRecord{
			ActionExecutionID: origID,
			State:             StateAbandoned,
		}
		c.journal.put(rec)
		return ConsumeResult{Outcome: OutcomeAbandoned, Record: rec}
	}
	if rec.State == StateCompleted || rec.State == StateFailed || rec.State == StateAbandoned {
		return ConsumeResult{Outcome: OutcomeNoAction, Record: rec}
	}
	if rec.CommittedCount() == 0 {
		rec.State = StateAbandoned
		c.journal.put(rec)
		return ConsumeResult{Outcome: OutcomeAbandoned, Record: rec}
	}
	// 补偿已越过不可逆点：继续完成，撤销不介入。
	return ConsumeResult{Outcome: OutcomeNoAction, Record: rec}
}

// fail 对无法关联补偿记录的错误直接返回。
func (c *Consumer) fail(evt ChangeEvent, err *ConsumeError) ConsumeResult {
	return ConsumeResult{Outcome: OutcomeError, Err: err}
}

// failWithRecord 在副作用提交前失败：记录 Failed 终态，保证不施加
// 任何副作用（全部不生效）。
func (c *Consumer) failWithRecord(evt ChangeEvent, rec *CompensationRecord, err *ConsumeError) ConsumeResult {
	if rec == nil {
		return c.fail(evt, err)
	}
	rec.State = StateFailed
	rec.Err = err
	c.journal.put(*rec)
	return ConsumeResult{Outcome: OutcomeError, Record: *rec, Err: err}
}

// validatePlan 对补偿计划做原子性结构校验。
func validatePlan(effects []SideEffect) string {
	if len(effects) == 0 {
		return "补偿计划为空，原子性要求无法满足"
	}
	for i, e := range effects {
		if !knownOps[e.Op] {
			return "副作用包含未知操作类型: " + string(e.Op)
		}
		if e.Op != OpDelete && e.Field == "" {
			return "副作用缺少目标字段"
		}
		if e.ObjectID == "" {
			return "副作用缺少目标对象"
		}
		_ = i
	}
	return ""
}
