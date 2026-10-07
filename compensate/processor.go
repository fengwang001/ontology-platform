package compensate

import (
	"context"
	"fmt"
)

// 消费结论常量。
const (
	OutcomeCompleted  = "COMPLETED"
	OutcomeDuplicate  = "DUPLICATE_SKIPPED"
	OutcomeSuperseded = "SUPERSEDED"
	OutcomeFailed     = "FAILED"
)

// HandleResult 是一条事件的消费结论。
type HandleResult struct {
	EventID string
	// Outcome: COMPLETED / DUPLICATE_SKIPPED / SUPERSEDED / FAILED
	Outcome string
	// Resumed 为 true 表示本次是崩溃后续作（记录了此前部分生效的边界）。
	Resumed bool
	Err     *TerminalError
}

// HistoryInspector 在崩溃续作时回答「这项副作用此前是否已经生效」。
// 默认实现直接探测 Effect.OwnedKey（定点 O(1)）；测试可注入历史缺失故障（E2）。
type HistoryInspector interface {
	EffectApplied(txn Txn, ev Event, fx Effect, index int) (bool, error)
}

// Processor 是变更流消费者。
type Processor struct {
	store     Store
	regs      *Registry
	audit     AuditLog
	history   HistoryInspector
	crashHook func(ev Event, effectIndex int) bool
}

// Config 构造处理器的输入。
type Config struct {
	Store   Store
	Specs   *Registry
	History HistoryInspector
}

// NewProcessor 构造消费者。History 为 nil 时使用 DefaultHistory。
func NewProcessor(cfg Config) *Processor {
	history := cfg.History
	if history == nil {
		history = DefaultHistory
	}
	return &Processor{store: cfg.Store, regs: cfg.Specs, audit: AuditLog{}, history: history}
}

// dedupMetrics 记录单次判定为回答「是否重复」所做的记录索引探测次数。
// 一次 Handle 对 recordKey(EventID) 恰好探测一次；该数字不随历史事件总量
// 变化，构成 O(1) 开销的可独立验证证据（测试读取并断言）。
type dedupMetrics struct{ probes int }

type countingTxn struct {
	Txn
	m *dedupMetrics
}

func (t countingTxn) Get(key string) ([]byte, error) {
	if len(key) >= len(keyRecord) && key[:len(keyRecord)] == keyRecord {
		t.m.probes++
	}
	return t.Txn.Get(key)
}

func terminal(class ErrorClass, ev Event, msg string) error {
	return &TerminalError{Class: class, Event: ev, msg: msg}
}

// Handle 消费一条变更事件。并发安全，可被同一 EventID 的重复投递并发调用。
//
// 执行分两阶段：
//  1. decide：一个可串行化事务完成去重判定与「领取/放弃」裁决（或识别续作）；
//  2. applyEffects：每项副作用各自在独立可串行化事务内提交，崩溃后重投即续作。
func (p *Processor) Handle(ctx context.Context, ev Event) HandleResult {
	// E3（最高优先级）：标识信息本身不足以判定关系，直接报告，不触碰任何副作用。
	if !ValidIdentity(ev) {
		return p.reportAmbiguous(ev, "empty EventID: identity cannot be related to any prior event")
	}
	spec, specOK := p.regs.Lookup(ev.ActionType)

	decision, err := p.decide(ev, specOK)
	if err != nil {
		return failedResult(ev, err)
	}
	switch decision.kind {
	case decideSuperseded:
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeSuperseded}
	case decideNoSpec:
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted}
	case decideDuplicateComplete:
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted}
	case decideDuplicateSuperseded:
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeSuperseded}
	case decideFrozen:
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed, Err: decision.terminal}
	}

	// decideNew 或 decideResume：逐项执行（续作只会补齐未生效项）。
	resumed := decision.kind == decideResume
	if termErr := p.applyEffects(ctx, ev, spec); termErr != nil {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed, Resumed: resumed, Err: termErr}
	}
	if termErr := p.markCompleted(ev); termErr != nil {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed, Resumed: resumed, Err: termErr}
	}
	return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted, Resumed: resumed}
}

func failedResult(ev Event, err error) HandleResult {
	if termErr, ok := err.(*TerminalError); ok {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed, Err: termErr}
	}
	return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed,
		Err: &TerminalError{Class: ErrorClass("INFRA"), Event: ev, msg: err.Error()}}
}

type decideKind int

const (
	decideNew decideKind = iota
	decideResume
	decideSuperseded
	decideNoSpec
	decideDuplicateComplete
	decideDuplicateSuperseded
	decideFrozen
)

type decision struct {
	kind     decideKind
	terminal *TerminalError
}

func (p *Processor) decide(ev Event, specOK bool) (decision, error) {
	for {
		metrics := &dedupMetrics{}
		var d decision
		err := p.store.Update(func(txn Txn) error {
			if pt, ok := txn.(predicateTxn); ok {
				pt.EnablePredicateLocksFor(keyRecord, keyUndo)
			}
			ct := countingTxn{Txn: txn, m: metrics}
			p.audit.Append(txn, AuditHandle, ev, map[string]string{
				"action_type": ev.ActionType,
				"payload_len": itoa(len(ev.Payload)),
			})

			raw, getErr := ct.Get(recordKey(ev.EventID))
			if getErr == nil {
				rec, decErr := decodeRecord(raw)
				if decErr != nil {
					p.auditDedup(txn, ev, metrics.probes, "EXISTING_RECORD_UNREADABLE", "")
					return terminal(ErrAmbiguousIdentity, ev,
						"existing record for EventID is unreadable; relation cannot be determined")
				}
				if rec.Fingerprint != Fingerprint(ev) {
					// 同 ID 不同内容：无法判断是损坏投递还是 ID 冲突复用 → E3。
					p.auditDedup(txn, ev, metrics.probes, "SAME_ID_DIFFERENT_CONTENT", string(rec.State))
					return terminal(ErrAmbiguousIdentity, ev,
						"same EventID previously handled with different content")
				}
				switch rec.State {
				case StateCompleted:
					p.auditDedup(txn, ev, metrics.probes, "DUPLICATE_DELIVERY_SKIP", string(rec.State))
					d = decision{kind: decideDuplicateComplete}
				case StateSuperseded:
					p.auditDedup(txn, ev, metrics.probes, "DUPLICATE_AFTER_SUPERSEDE_SKIP", string(rec.State))
					d = decision{kind: decideDuplicateSuperseded}
				case StateFailed:
					// 冻结边界：重复投递原样报告同一类错误，不扩大副作用。
					p.auditDedup(txn, ev, metrics.probes, "DUPLICATE_AT_FROZEN_BOUNDARY", string(rec.State))
					d = decision{kind: decideFrozen,
						terminal: &TerminalError{Class: rec.FailedClass, Event: ev, msg: rec.FailedReason}}
				case StateClaimed:
					p.auditDedup(txn, ev, metrics.probes, "RESUME_PARTIAL_COMPENSATION", string(rec.State))
					p.audit.Append(txn, AuditResume, ev, map[string]string{
						"applied_prefix": prefixLen(rec),
					})
					d = decision{kind: decideResume}
				default:
					return terminal(ErrAmbiguousIdentity, ev, "unknown record state: "+string(rec.State))
				}
				return nil
			}
			if getErr != ErrNotFound {
				return getErr
			}

			// 首次见到该 EventID：与撤销标记在同一事务内原子裁决。
			p.auditDedup(txn, ev, metrics.probes, "NEW_EVENT", "")
			if _, undoErr := txn.Get(undoKey(ev.EventID)); undoErr == nil {
				rec := &Record{State: StateSuperseded, EventID: ev.EventID,
					ActionType: ev.ActionType, Fingerprint: Fingerprint(ev)}
				txn.Put(recordKey(ev.EventID), encodeRecord(rec))
				p.audit.Append(txn, AuditSuperseded, ev, map[string]string{
					"rule": "undo-before-claim wins; compensation abandoned permanently",
				})
				d = decision{kind: decideSuperseded}
				return nil
			}
			rec := &Record{
				State:       StateClaimed,
				EventID:     ev.EventID,
				ActionType:  ev.ActionType,
				Fingerprint: Fingerprint(ev),
				EffectCount: 0,
				Applied:     []bool{},
			}
			if specOK {
				if spec, ok := p.regs.Lookup(ev.ActionType); ok {
					rec.EffectCount = len(spec.Effects)
					rec.Applied = make([]bool, len(spec.Effects))
				}
			}
			txn.Put(recordKey(ev.EventID), encodeRecord(rec))
			p.audit.Append(txn, AuditClaim, ev, map[string]string{
				"rule":    "claimed; undo after this point can no longer cancel compensation",
				"effects": itoa(rec.EffectCount),
			})
			if !specOK {
				rec.State = StateCompleted
				txn.Put(recordKey(ev.EventID), encodeRecord(rec))
				p.audit.Append(txn, AuditComplete, ev, map[string]string{"effects": "0"})
				d = decision{kind: decideNoSpec}
				return nil
			}
			d = decision{kind: decideNew}
			return nil
		})
		if err == ErrConflict {
			continue
		}
		return d, err
	}
}

func (p *Processor) auditDedup(txn Txn, ev Event, probes int, conclusion, priorState string) {
	detail := map[string]string{
		"event_id":   ev.EventID,
		"probes":     itoa(probes),
		"conclusion": conclusion,
	}
	if priorState != "" {
		detail["prior_state"] = priorState
	}
	p.audit.Append(txn, AuditDedup, ev, detail)
}

func prefixLen(rec *Record) string {
	n := 0
	for i := range rec.Applied {
		if rec.Applied[i] {
			n = i + 1
		}
	}
	return itoa(n)
}

// applyEffects 逐项提交副作用。每项在独立可串行化事务内提交：
// 提交前崩溃 ⇒ 该项与后续项均未生效；提交后崩溃 ⇒ 续作按独占键识别并跳过。
// 因此任意崩溃时刻都存在确定边界（已生效前缀），重投绝不会重复施加。
//
// 无关并发修改共存原则：事务的读集合只包含 (1) 补偿记录、(2) 本项副作用的独占键、
// (3) 后续项独占键（顺序原子性校验）、(4) TargetExists 显式选择的目标键。
// 其他无关事件修改目标对象的「其他字段」不在读集合中，不会导致续作中止；
// 仅当它们恰好改动这些依赖键（真正冲突）时才由 OCC 串行化排序后安全重试。
func (p *Processor) applyEffects(ctx context.Context, ev Event, spec CompensationSpec) *TerminalError {
	for i, fx := range spec.Effects {
		for {
			var frozen *TerminalError
			err := p.store.Update(func(txn Txn) error {
				raw, err := txn.Get(recordKey(ev.EventID))
				if err != nil {
					return err
				}
				rec, err := decodeRecord(raw)
				if err != nil {
					return terminal(ErrAmbiguousIdentity, ev, "record unreadable before effect "+itoa(i))
				}
				if rec.State == StateFailed {
					frozen = &TerminalError{Class: rec.FailedClass, Event: ev, msg: rec.FailedReason}
					return nil
				}
				if i < len(rec.Applied) && rec.Applied[i] {
					return nil // 位图确认已生效（含本事务重试前已在赢者事务提交的情况）
				}
				applied, histErr := p.history.EffectApplied(txn, ev, fx, i)
				if histErr != nil {
					// E2：部分生效后需要续作，但判定边界所需的历史记录缺失。
					return p.freeze(txn, ev, rec, ErrHistoryMissing,
						"cannot determine whether effect "+itoa(i)+" already applied: "+histErr.Error())
				}
				if applied {
					rec.Applied[i] = true
					txn.Put(recordKey(ev.EventID), encodeRecord(rec))
					p.audit.Append(txn, AuditEffect, ev, map[string]string{
						"index": itoa(i), "conclusion": "ALREADY_APPLIED_SKIP",
					})
					return nil
				}
				// E4：顺序原子性校验——记录与实际状态不一致（后继已生效而前驱缺失）。
				for j := i + 1; j < len(spec.Effects); j++ {
					done, checkErr := p.history.EffectApplied(txn, ev, spec.Effects[j], j)
					if checkErr != nil {
						return p.freeze(txn, ev, rec, ErrHistoryMissing,
							"history unavailable during atomicity check: "+checkErr.Error())
					}
					if done {
						return p.freeze(txn, ev, rec, ErrAtomicityViolation,
							"effect "+itoa(j)+" applied while predecessor "+itoa(i)+" is not")
					}
				}
				// E1：目标已不存在或已撤销（在 E3/E2/E4 之后检查，保证优先级）。
				if spec.TargetExists != nil && !spec.TargetExists(txn, ev) {
					return p.freeze(txn, ev, rec, ErrTargetGone,
						"compensation target missing or revoked before effect "+itoa(i))
				}
				if err := fx.Apply(ctx, txn, ev, i); err != nil {
					return err
				}
				rec.Applied[i] = true
				txn.Put(recordKey(ev.EventID), encodeRecord(rec))
				p.audit.Append(txn, AuditEffect, ev, map[string]string{
					"index": itoa(i), "conclusion": "NEWLY_COMMITTED",
				})
				return nil
			})
			if err == ErrConflict {
				continue // 与无关修改/重复投递竞争：重读快照重新判定，安全无副作用重复
			}
			if err != nil {
				if termErr, ok := err.(*TerminalError); ok {
					return termErr
				}
				// 基础设施错误：此刻无法确认边界，保持现状；事件重投走续作核实，
				// 已提交的前缀不会被重复施加。
				return &TerminalError{Class: ErrorClass("INFRA"), Event: ev, msg: err.Error()}
			}
			if frozen != nil {
				return frozen
			}
			if p.crashHook != nil && p.crashHook(ev, i) {
				// 事务已提交、消费者此刻「崩溃」：记录停留在 CLAIMED + 前缀位图，
				// 已提交前缀对后续重放可由独占键精确识别，绝不重复施加。
				return &TerminalError{Class: ErrorClass("INJECTED_CRASH"), Event: ev,
					msg: "crash after effect " + itoa(i) + " committed"}
			}
			break
		}
	}
	return nil
}

func (p *Processor) markCompleted(ev Event) *TerminalError {
	for {
		err := p.store.Update(func(txn Txn) error {
			raw, err := txn.Get(recordKey(ev.EventID))
			if err != nil {
				return err
			}
			rec, err := decodeRecord(raw)
			if err != nil {
				return terminal(ErrAmbiguousIdentity, ev, "record unreadable at completion")
			}
			if rec.State == StateFailed {
				return &TerminalError{Class: rec.FailedClass, Event: ev, msg: rec.FailedReason}
			}
			// 幂等完成：已是 COMPLETED（并发重复投递/续作者先完成）则不再
			// 重复写状态、不重复追加 COMPLETE 审计——保证完成事件全库恰一次。
			if rec.State == StateCompleted {
				return nil
			}
			rec.State = StateCompleted
			txn.Put(recordKey(ev.EventID), encodeRecord(rec))
			p.audit.Append(txn, AuditComplete, ev, map[string]string{"effects": itoa(rec.EffectCount)})
			return nil
		})
		if err == ErrConflict {
			continue
		}
		if err != nil {
			if termErr, ok := err.(*TerminalError); ok {
				return termErr
			}
			return &TerminalError{Class: ErrorClass("INFRA"), Event: ev, msg: err.Error()}
		}
		return nil
	}
}

// freeze 在同一事务内把记录置为 FAILED 并记录唯一错误分类，然后返回事务错误。
// 冻结语义：已生效的副作用保持不动，未生效的绝不补做；后续重复投递原样返回同一错误。
func (p *Processor) freeze(txn Txn, ev Event, rec *Record, class ErrorClass, msg string) error {
	rec.State = StateFailed
	rec.FailedClass = class
	rec.FailedReason = msg
	txn.Put(recordKey(ev.EventID), encodeRecord(rec))
	p.audit.Append(txn, AuditTerminalError, ev, map[string]string{
		"class":           string(class),
		"priority":        itoa(ClassPriority(class)),
		"applied_prefix":  prefixLen(rec),
		"boundary_frozen": "true",
		"message":         msg,
	})
	return &TerminalError{Class: class, Event: ev, msg: msg}
}

// reportAmbiguous 在一个事务内仅追加 E3 审计后返回（不写补偿记录、不施加副作用）。
func (p *Processor) reportAmbiguous(ev Event, msg string) HandleResult {
	_ = p.store.Update(func(txn Txn) error {
		p.audit.Append(txn, AuditTerminalError, ev, map[string]string{
			"class": string(ErrAmbiguousIdentity), "priority": itoa(ClassPriority(ErrAmbiguousIdentity)),
			"message": msg,
		})
		return nil
	})
	return HandleResult{EventID: ev.EventID, Outcome: OutcomeFailed,
		Err: &TerminalError{Class: ErrAmbiguousIdentity, Event: ev, msg: msg}}
}

// UndoOutcome 是撤销请求与补偿之间的裁决结果。
type UndoOutcome string

const (
	// UndoWins 撤销在补偿领取之前到达：补偿被永久放弃。
	UndoWins UndoOutcome = "UNDO_WINS_ABANDONED"
	// UndoTooLate 补偿已经领取：策略规定补偿必须继续完成，撤销不能中途打断。
	UndoTooLate UndoOutcome = "UNDO_TOO_LATE_COMPENSATION_CONTINUES"
)

// UndoArrived 登记「原始动作的撤销请求到达」。幂等。
//
// 与补偿的裁决规则（不随撤销到达时刻产生不一致）：
//   - 记录不存在或仍可裁决：写入撤销标记；此后的 Handle 领取时看到标记 → SUPERSEDED。
//   - 记录已是 CLAIMED（补偿已领取，哪怕一项副作用都还没提交）：UndoTooLate，
//     补偿继续直至完成。领取点是唯一的线性化分界，撤销与领取在同一可串行化
//     存储上排序，谁先提交谁生效，不存在「取决于具体时刻」的模糊窗口。
//   - 记录 COMPLETED：UndoTooLate。SUPERSEDED/FAILED：撤销不再改变任何状态。
func (p *Processor) UndoArrived(ev Event) UndoOutcome {
	if !ValidIdentity(ev) {
		return UndoTooLate // 无身份事件不参与裁决
	}
	outcome := UndoWins
	for {
		err := p.store.Update(func(txn Txn) error {
			if pt, ok := txn.(predicateTxn); ok {
				pt.EnablePredicateLocksFor(keyRecord, keyUndo)
			}
			if raw, err := txn.Get(recordKey(ev.EventID)); err == nil {
				rec, decErr := decodeRecord(raw)
				if decErr == nil {
					switch rec.State {
					case StateClaimed, StateCompleted, StateFailed:
						outcome = UndoTooLate
						p.audit.Append(txn, AuditSuperseded, ev, map[string]string{
							"rule": "undo after claim: compensation continues to completion",
						})
						return nil
					case StateSuperseded:
						outcome = UndoWins
						return nil
					default:
						// 未知状态：不允许穿透写入撤销标记，冲突重试。
						return ErrConflict
					}
				}
				return ErrConflict // 记录损坏：冲突重试，绝不猜测裁决
			}
			txn.Put(undoKey(ev.EventID), []byte("1"))
			p.audit.Append(txn, AuditSuperseded, ev, map[string]string{
				"rule": "undo marker installed before claim",
			})
			outcome = UndoWins
			return nil
		})
		if err == ErrConflict {
			continue
		}
		return outcome
	}
}

var _ = fmt.Sprintf
