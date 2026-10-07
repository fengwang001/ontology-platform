package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// PropEvidence 记录一次判定中针对单个不可合并属性使用的证据：
// 判定只读取该属性的“最后变更版本号”，与历史版本总数无关。
type PropEvidence struct {
	// PropVersion 该属性最后一次实际变更时的实例版本。
	PropVersion uint64
	// EqualToCurrent 声明新值与当前值是否相等（等价写）。
	EqualToCurrent bool
}

// MergeEvidence 记录一个可合并属性的合并计算依据。
type MergeEvidence struct {
	Rule     string
	Current  Value
	Declared Value
	Merged   Value
}

// LogEntry 完整记录一次写入的判定与结果，可供独立重放核验。
type LogEntry struct {
	Seq     uint64
	Request WriteRequest
	Outcome Outcome
	// NoOp 为 true 表示提交未产生任何实际状态变化（等价写），版本号未推进。
	NoOp bool
	// BaseVersion 与 ResultVersion 分别为判定时实例版本与提交后版本。
	BaseVersion   uint64
	ResultVersion uint64
	// ConflictProperty 触发拒绝的属性（仅 OutcomeRejectedConflict）。
	ConflictProperty string
	// PropEvidence 按属性名记录不可合并属性的判定证据。
	PropEvidence map[string]PropEvidence
	// MergeEvidence 按属性名记录可合并属性的合并依据。
	MergeEvidence map[string]MergeEvidence
}

// CommitLog 是只追加的判定日志。
type CommitLog struct {
	mu      sync.Mutex
	entries []LogEntry
}

func newCommitLog() *CommitLog {
	return &CommitLog{}
}

// append 追加一条日志，由 Store 在持有实例锁时调用。
func (l *CommitLog) append(e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = uint64(len(l.entries)) + 1
	l.entries = append(l.entries, e)
}

// Entries 返回日志条目副本（按提交顺序）。
func (l *CommitLog) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Verify 仅依据日志内容、schema 与初始状态，独立重放并核验每一条
// 判定记录：判定结果、证据、版本号必须全部吻合，否则返回错误。
// 它不读取 Store 的任何内部状态，是“判定开销与历史无关”之外的
// 另一类可供验证的证据：任何第三方都可用日志重放确认判定正确性。
func Verify(typ *ObjectType, initial map[string]Value, retention uint64, entries []LogEntry) error {
	if err := typ.Validate(); err != nil {
		return err
	}
	// 重放状态：版本、属性值、每属性最后变更版本。
	var version uint64
	values := make(map[string]Value, len(initial))
	for k, v := range initial {
		values[k] = v
	}
	propVersion := make(map[string]uint64, len(initial))

	for _, e := range entries {
		req := e.Request
		if e.BaseVersion != version {
			return fmt.Errorf("seq=%d: 记录的实例版本 %d 与重放版本 %d 不符", e.Seq, e.BaseVersion, version)
		}
		props := make([]string, 0, len(req.Changes))
		for name := range req.Changes {
			props = append(props, name)
		}
		sort.Strings(props)

		// 重放第一步：基线判定。
		minBase := uint64(0)
		if retention > 0 && version > retention {
			minBase = version - retention
		}
		if req.BaseVersion > version || req.BaseVersion < minBase {
			if e.Outcome != OutcomeRejectedStaleBaseline {
				return fmt.Errorf("seq=%d: 期望基线冲突拒绝，记录为 %s", e.Seq, e.Outcome)
			}
			if e.ResultVersion != version {
				return fmt.Errorf("seq=%d: 拒绝后版本发生变化", e.Seq)
			}
			continue
		}

		// 重放第二步：不可合并属性冲突判定，并核对证据。
		conflictProp := ""
		for _, name := range props {
			p := typ.property(name)
			if p == nil {
				return fmt.Errorf("seq=%d: 未知属性 %q", e.Seq, name)
			}
			if p.Mergeable {
				continue
			}
			ev, ok := e.PropEvidence[name]
			if !ok {
				return fmt.Errorf("seq=%d: 缺少属性 %q 的判定证据", e.Seq, name)
			}
			want := PropEvidence{
				PropVersion:    propVersion[name],
				EqualToCurrent: Equal(values[name], req.Changes[name]),
			}
			if ev != want {
				return fmt.Errorf("seq=%d: 属性 %q 证据 %+v 与重放 %+v 不符", e.Seq, name, ev, want)
			}
			if want.PropVersion > req.BaseVersion && !want.EqualToCurrent && conflictProp == "" {
				conflictProp = name
			}
		}
		if conflictProp != "" {
			if e.Outcome != OutcomeRejectedConflict || e.ConflictProperty != conflictProp {
				return fmt.Errorf("seq=%d: 期望因属性 %q 整体拒绝，记录为 %s/%q", e.Seq, conflictProp, e.Outcome, e.ConflictProperty)
			}
			if e.ResultVersion != version {
				return fmt.Errorf("seq=%d: 拒绝后版本发生变化", e.Seq)
			}
			continue
		}

		// 重放第三步：合并与应用，核对合并证据。
		if e.Outcome != OutcomeCommitted {
			return fmt.Errorf("seq=%d: 期望提交，记录为 %s", e.Seq, e.Outcome)
		}
		pending := make(map[string]Value)
		for _, name := range props {
			p := typ.property(name)
			if !p.Mergeable {
				if !Equal(req.Changes[name], values[name]) {
					pending[name] = req.Changes[name]
				}
				continue
			}
			rule := RuleByName(p.MergeRule)
			merged := rule.Join(values[name], req.Changes[name])
			me, ok := e.MergeEvidence[name]
			if !ok {
				return fmt.Errorf("seq=%d: 缺少属性 %q 的合并证据", e.Seq, name)
			}
			if me.Rule != rule.Name() || !Equal(me.Current, values[name]) ||
				!Equal(me.Declared, req.Changes[name]) || !Equal(me.Merged, merged) {
				return fmt.Errorf("seq=%d: 属性 %q 合并证据与重放不符", e.Seq, name)
			}
			if !Equal(merged, values[name]) {
				pending[name] = merged
			}
		}
		if len(pending) == 0 {
			if !e.NoOp || e.ResultVersion != version {
				return fmt.Errorf("seq=%d: 期望 no-op 提交且版本不变", e.Seq)
			}
			continue
		}
		if e.NoOp {
			return fmt.Errorf("seq=%d: 存在实际变更却记录为 no-op", e.Seq)
		}
		version++
		for name, v := range pending {
			values[name] = v
			propVersion[name] = version
		}
		if e.ResultVersion != version {
			return fmt.Errorf("seq=%d: 提交后版本 %d 与重放版本 %d 不符", e.Seq, e.ResultVersion, version)
		}
	}
	return nil
}
