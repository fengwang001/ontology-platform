package idx

import (
	"errors"
	"sort"
	"sync"
)

// objProp 是 (对象, 属性) 寄存器的键。
type objProp struct {
	obj  string
	prop string
}

// lwwEntry 是寄存器中当前胜出的取值（Last-Writer-Wins，
// 按 (Version, ID) 全序判定）。
type lwwEntry struct {
	version uint64
	id      EventID
	value   string
	null    bool
}

func lessEntry(a, b lwwEntry) bool {
	if a.version != b.version {
		return a.version < b.version
	}
	return a.id < b.id
}

// Validator 在切换提交前校验既有数据是否满足新依据字段的约束。
// 返回非 nil 错误则切换整体回滚。
type Validator func(regs map[objProp]lwwEntry, newBasis string) error

// RequireNonNullForAll 是默认校验器：所有已知对象在新依据字段上
// 都必须有非空取值。
func RequireNonNullForAll(regs map[objProp]lwwEntry, newBasis string) error {
	objs := make(map[string]struct{})
	for key := range regs {
		objs[key.obj] = struct{}{}
	}
	for obj := range objs {
		entry, ok := regs[objProp{obj: obj, prop: newBasis}]
		if !ok || entry.null {
			return errors.New("object " + obj + " has no non-null value for " + newBasis)
		}
	}
	return nil
}

// switchState 描述一次进行中的索引依据字段切换。
type switchState struct {
	newBasis  string
	candidate *epoch  // 正在构建的新索引版本
	buffered  []Event // 切换期间到达、尚未归属的事件
}

// Store 是增量索引子系统本体。所有公开方法可并发调用；
// 内部由单个 RWMutex 串行化，保证任意并发操作集合的可观察结果
// 等价于某个全局串行顺序（可线性化）。
type Store struct {
	mu        sync.RWMutex
	types     *TypeRegistry
	regs      map[objProp]lwwEntry
	active    *epoch
	retired   []*epoch
	switching *switchState
	audit     AuditSink
	validator Validator
	nextEpoch int
}

// Option 配置 Store。
type Option func(*Store)

// WithAudit 配置审计输出。
func WithAudit(sink AuditSink) Option {
	return func(s *Store) { s.audit = sink }
}

// WithValidator 配置切换校验器。
func WithValidator(v Validator) Option {
	return func(s *Store) { s.validator = v }
}

// NewStore 创建子系统；basis 为初始索引依据字段，必须在最新类型
// 定义中处于有效状态。
func NewStore(types *TypeRegistry, basis string, opts ...Option) (*Store, error) {
	def, ok := types.latest(basis)
	if !ok {
		return nil, errors.New("idx: basis property " + basis + " not defined")
	}
	if def.Status != PropActive {
		return nil, errors.New("idx: basis property " + basis + " is not active")
	}
	s := &Store{
		types:     types,
		regs:      make(map[objProp]lwwEntry),
		audit:     nopAudit{},
		validator: RequireNonNullForAll,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.audit == nil {
		s.audit = nopAudit{}
	}
	if s.validator == nil {
		s.validator = RequireNonNullForAll
	}
	s.active = newEpoch(s.nextEpoch, basis)
	s.nextEpoch++
	return s, nil
}

// Ingest 消费一条变更事件。事件可能乱序或重复到达。
//
// 归属规则：切换未进行时事件直接应用；切换进行中事件进入缓冲区，
// 待提交或回滚时按"事件针对的属性字段"归入对应索引版本——与到达
// 消费端的物理时序无关。
func (s *Store) Ingest(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	def, defined := s.types.defAt(ev.Property, ev.Version)
	_ = def
	if latest, ok := s.types.latest(ev.Property); ok &&
		latest.Status == PropDeprecated && latest.ReplacedBy == "" &&
		ev.Property == s.activeBasisLocked() {
		errs = append(errs, fmtErrorf(ev, ErrDeprecatedNoReplacement))
	}
	if !defined {
		errs = append(errs, fmtErrorf(ev, ErrPropertyNotDefined))
	}
	if err := pickError(errs); err != nil {
		s.audit.Record(AuditEntry{
			Kind:    AuditRejected,
			Event:   &ev,
			EpochID: s.activeEpochIDLocked(),
			Basis:   s.activeBasisLocked(),
			Detail:  err.Error(),
		})
		return err
	}

	if s.switching != nil {
		s.switching.buffered = append(s.switching.buffered, ev)
		s.audit.Record(AuditEntry{
			Kind:    AuditBuffered,
			Event:   &ev,
			EpochID: -1,
			Basis:   s.switching.newBasis,
			Detail:  "buffered during switch to " + s.switching.newBasis,
		})
		return nil
	}

	s.applyLocked(ev, s.active)
	return nil
}

// BeginSwitch 启动索引依据字段切换。切换期间事件被缓冲、查询返回
// ErrSwitchInProgress，查询端不会观察到新旧依据混杂的中间状态。
func (s *Store) BeginSwitch(newBasis string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.switching != nil {
		return errors.New("idx: switch already in progress")
	}
	def, ok := s.types.latest(newBasis)
	switch {
	case !ok:
		return fmtErrorf(Event{Property: newBasis}, ErrPropertyNotDefined)
	case def.Status == PropDeprecated && def.ReplacedBy == "":
		return fmtErrorf(Event{Property: newBasis}, ErrDeprecatedNoReplacement)
	case def.Status != PropActive:
		return errors.New("idx: new basis property " + newBasis + " is not active")
	}

	candidate := newEpoch(s.nextEpoch, newBasis)
	// 从寄存器回填：新依据字段的既有最终取值全部进入候选索引。
	for key, entry := range s.regs {
		if key.prop == newBasis {
			candidate.set(key.obj, entry.value, entry.null)
		}
	}
	s.switching = &switchState{newBasis: newBasis, candidate: candidate}
	s.audit.Record(AuditEntry{
		Kind:    AuditSwitchBegin,
		EpochID: candidate.id,
		Basis:   newBasis,
		Detail:  "switch from " + s.active.basis + " to " + newBasis,
	})
	return nil
}

// CommitSwitch 校验并提交切换。校验失败时整体回滚并返回
// ErrSwitchValidationFailed；缓冲事件按归属规则分别归入新旧版本，
// 不会因切换失败而丢失。
func (s *Store) CommitSwitch() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.switching == nil {
		return errors.New("idx: no switch in progress")
	}
	sw := s.switching
	if err := s.validator(s.regs, sw.newBasis); err != nil {
		s.rollbackLocked("validation failed: " + err.Error())
		return fmtErrorf(Event{Property: sw.newBasis}, ErrSwitchValidationFailed)
	}
	old := s.active
	for _, ev := range sw.buffered {
		if ev.Property == sw.newBasis {
			s.applyLocked(ev, sw.candidate)
		} else {
			// 旧依据字段的事件仍归入旧索引版本（保留完整历史供审计）。
			s.applyLocked(ev, old)
		}
	}
	s.retired = append(s.retired, old)
	s.active = sw.candidate
	s.nextEpoch++
	s.switching = nil
	s.audit.Record(AuditEntry{
		Kind:    AuditSwitchCommit,
		EpochID: s.active.id,
		Basis:   s.active.basis,
		Detail:  "committed; old epoch retired",
	})
	return nil
}

// RollbackSwitch 主动放弃进行中的切换，缓冲事件重新归入旧版本。
func (s *Store) RollbackSwitch() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.switching == nil {
		return errors.New("idx: no switch in progress")
	}
	s.rollbackLocked("manual rollback")
	return nil
}

// rollbackLocked 整体回滚到切换前状态。缓冲事件重新归入旧版本：
// 旧依据字段的事件直接作用于旧索引继续生效；新依据字段的事件写入
// 寄存器（不丢失），待未来再次切换时经回填生效。
func (s *Store) rollbackLocked(reason string) {
	sw := s.switching
	for _, ev := range sw.buffered {
		if ev.Property == s.active.basis {
			s.applyLocked(ev, s.active)
		} else {
			s.applyLocked(ev, nil)
		}
		s.audit.Record(AuditEntry{
			Kind:    AuditClassify,
			Event:   &ev,
			EpochID: s.active.id,
			Basis:   s.active.basis,
			Detail:  "re-attributed to old epoch after rollback",
		})
	}
	s.switching = nil
	s.audit.Record(AuditEntry{
		Kind:    AuditSwitchRollback,
		EpochID: s.active.id,
		Basis:   s.active.basis,
		Detail:  reason,
	})
}

// Query 按属性取值定位对象，返回有序对象 ID 列表。
// 切换进行中返回 ErrSwitchInProgress，绝不返回新旧依据混杂的结果。
func (s *Store) Query(value string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.switching != nil {
		err := ErrSwitchInProgress
		s.audit.Record(AuditEntry{
			Kind:    AuditQuery,
			EpochID: -1,
			Basis:   s.switching.newBasis,
			Detail:  "rejected: " + err.Error(),
		})
		return nil, err
	}
	res := sortedKeys(s.active.lookup(value))
	s.audit.Record(AuditEntry{
		Kind:    AuditQuery,
		EpochID: s.active.id,
		Basis:   s.active.basis,
		Detail:  "value=" + value,
	})
	return res, nil
}

// applyLocked 将事件应用到寄存器，并在其赢得 LWW 且命中目标索引
// 版本的依据字段时更新该索引。target 为 nil 表示只写寄存器。
func (s *Store) applyLocked(ev Event, target *epoch) {
	key := objProp{obj: ev.ObjectID, prop: ev.Property}
	cand := lwwEntry{version: ev.Version, id: ev.ID, value: ev.Value, null: ev.Null}
	if cur, ok := s.regs[key]; ok && !lessEntry(cur, cand) {
		kind := AuditStale
		if cur.id == ev.ID {
			kind = AuditDuplicate
		}
		s.audit.Record(AuditEntry{
			Kind:    kind,
			Event:   &ev,
			EpochID: epochIDOf(target),
			Basis:   basisOf(target),
			Detail:  "dominated by existing entry",
		})
		return
	}
	s.regs[key] = cand
	if target != nil && ev.Property == target.basis {
		target.set(ev.ObjectID, ev.Value, ev.Null)
	}
	s.audit.Record(AuditEntry{
		Kind:    AuditApplied,
		Event:   &ev,
		EpochID: epochIDOf(target),
		Basis:   basisOf(target),
		Detail:  "register updated",
	})
}

func (s *Store) activeBasisLocked() string { return s.active.basis }

func (s *Store) activeEpochIDLocked() int { return s.active.id }

func epochIDOf(e *epoch) int {
	if e == nil {
		return -1
	}
	return e.id
}

func basisOf(e *epoch) string {
	if e == nil {
		return ""
	}
	return e.basis
}

func fmtErrorf(ev Event, sentinel error) error {
	return &eventError{ev: ev, sentinel: sentinel}
}

// eventError 为错误携带触发事件的上下文，同时保持 errors.Is 可判定。
type eventError struct {
	ev       Event
	sentinel error
}

func (e *eventError) Error() string {
	return e.sentinel.Error() + " (object=" + e.ev.ObjectID + " property=" + e.ev.Property + ")"
}

func (e *eventError) Unwrap() error { return e.sentinel }

// ActiveEpoch 返回当前活跃索引版本号与依据字段（主要用于测试与审计核对）。
func (s *Store) Active() (epochID int, basis string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.id, s.active.basis
}

// sortedKeys 将对象集合转为有序切片，保证查询结果确定性。
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
