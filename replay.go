package ontology

import (
	"errors"
	"fmt"
	"sort"
)

// 重建错误分类。同一次重建请求若同时满足多类错误条件，
// 按下列声明顺序（自上而下）只报告优先级最高的一类：
//
//  1. ErrAmbiguousOrder —— 事件流存在无法确定先后顺序的并列记录
//  2. ErrCutoffBeforeFirstEvent —— 截止时刻早于首个事件
//  3. ErrUndefinedTargetType —— 演变目标类型在当时尚未定义
//  4. ErrInvalidEvolutionPath —— 演变路径与当时继承关系矛盾
var (
	ErrAmbiguousOrder         = errors.New("ontology: 事件流中存在无法确定先后顺序的并列记录")
	ErrCutoffBeforeFirstEvent = errors.New("ontology: 截止时刻早于实例首个事件")
	ErrUndefinedTargetType    = errors.New("ontology: 目标类型在该事件发生时尚未定义")
	ErrInvalidEvolutionPath   = errors.New("ontology: 类型演变路径与当时继承关系矛盾")
)

// ErrInstanceNotFound 表示实例不存在（不属于上述四类重建错误）。
var ErrInstanceNotFound = errors.New("ontology: 实例不存在")

// 追加期错误（不属于重建错误分类，仅在 Append 时可能返回）。
var (
	// ErrOutOfRange 属性取值超出当时生效类型定义下的取值范围。
	ErrOutOfRange = errors.New("ontology: 属性取值超出当时生效的取值范围")
	// ErrFirstEventNotCreated 事件流的首个事件不是实例创建事件。
	ErrFirstEventNotCreated = errors.New("ontology: 首个事件必须是实例创建事件")
)

// State 是重建出的实例状态：当前对象类型与当前生效的属性取值。
type State struct {
	TypeID string
	Props  map[string]Value
}

// clone 深拷贝状态。
func (s State) clone() State {
	props := make(map[string]Value, len(s.Props))
	for k, v := range s.Props {
		props[k] = v
	}
	return State{TypeID: s.TypeID, Props: props}
}

// RebuildStats 描述一次重建的实际开销，用于独立验证
// “重建成本不随类型演变事件总数线性增长”。
type RebuildStats struct {
	EventsTotal    int  // 截止时刻之前的相关事件总数
	EventsReplayed int  // 本次实际逐条重放的事件数（快照之后的后缀）
	EvolutionsSeen int  // 本次重放后缀中遇到的类型演变事件数
	SnapshotUsed   bool // 是否命中快照
}

// TraceEntry 记录重建过程中对单个事件的判定依据与结论。
type TraceEntry struct {
	Seq         int
	Time        int64
	Kind        EventKind
	RuleVersion int // 解释该事件所依据的规则版本序号，-1 表示无生效版本
	Action      string
}

// DecisionTrace 是一次重建的完整判定记录，可序列化存档以便事后核查。
type DecisionTrace struct {
	InstanceID string
	Cutoff     int64
	Entries    []TraceEntry
	Final      State
	Err        error
}

// Replay 是对一段原始事件列表的纯函数式完整重放（不使用快照），
// 按错误优先级依次检查并列记录、截止时刻，再逐事件解释。
// 供导入校验与测试对照使用。
func Replay(events []Event, cutoff int64, rules *RuleStore) (State, error) {
	st, _, err := replayChecked(events, cutoff, rules, nil)
	return st, err
}

// firstOrderViolation 返回首个违反“时刻严格递增”的事件下标，无则返回 -1。
// 并列（时刻相等）或倒退都意味着记录顺序与逻辑时刻相互矛盾，
// 无法确定先后，统一按 ErrAmbiguousOrder 处理。
func firstOrderViolation(events []Event) int {
	for i := 1; i < len(events); i++ {
		if events[i].Time <= events[i-1].Time {
			return i
		}
	}
	return -1
}

// replayChecked 执行带错误优先级的完整检查与重放：
// 先查并列记录（仅当违规事件落在截止时刻之前），再查截止时刻，
// 最后按记录顺序逐事件解释。
func replayChecked(events []Event, cutoff int64, rules *RuleStore, sink *[]TraceEntry) (State, RebuildStats, error) {
	var stats RebuildStats
	if v := firstOrderViolation(events); v >= 0 && events[v].Time <= cutoff {
		return State{}, stats, fmt.Errorf("事件序号 %d 与前一事件时刻并列或倒退: %w", v, ErrAmbiguousOrder)
	}
	if len(events) == 0 || cutoff < events[0].Time {
		return State{}, stats, fmt.Errorf("cutoff=%d: %w", cutoff, ErrCutoffBeforeFirstEvent)
	}
	// 时刻严格递增（已校验），截止前缀连续，可二分。
	n := sort.Search(len(events), func(i int) bool { return events[i].Time > cutoff })
	stats.EventsTotal = n
	stats.EventsReplayed = n
	st, evolutions, err := replayPrefix(events[:n], rules, State{}, sink)
	stats.EvolutionsSeen = evolutions
	if err != nil {
		return State{}, stats, err
	}
	return st, stats, nil
}

// replayPrefix 从起始状态 start 出发，按顺序解释 events 中的每个事件。
// 返回终态与其中类型演变事件的个数。lenient 语义：取值不合规的赋值
// 事件被忽略（正常经 Append 写入的事件流不会包含此类事件，
// 该分支仅为导入数据兜底）。
func replayPrefix(events []Event, rules *RuleStore, start State, sink *[]TraceEntry) (State, int, error) {
	m := machine{typ: start.TypeID, props: make(map[string]Value, len(start.Props))}
	for k, v := range start.Props {
		m.props[k] = v
	}
	evolutions := 0
	for i := range events {
		if events[i].Kind == EvTypeEvolve {
			evolutions++
		}
		if err := m.apply(events[i], rules, false, sink); err != nil {
			return State{}, evolutions, err
		}
	}
	return State{TypeID: m.typ, Props: m.props}, evolutions, nil
}

// machine 是重放状态机：当前对象类型 + 当前生效属性值。
type machine struct {
	typ   string
	props map[string]Value
}

// apply 解释单个事件。strict 为 true 时（追加路径），不合规赋值返回
// ErrOutOfRange；否则（重建路径）忽略该赋值。类型演变事件触发
// “即时重校验”：所有留存取值按演变后的类型在事件时刻生效的规则版本
// 重新校验，不再合规的取值被丢弃。该处理只依赖事件记录本身，
// 与事件到达的物理顺序无关。
func (m *machine) apply(ev Event, rules *RuleStore, strict bool, sink *[]TraceEntry) error {
	rv, rvIdx := rules.EffectiveAt(ev.Time)
	note := ""
	defer func() {
		if sink != nil {
			*sink = append(*sink, TraceEntry{
				Seq: ev.Seq, Time: ev.Time, Kind: ev.Kind, RuleVersion: rvIdx, Action: note,
			})
		}
	}()
	switch ev.Kind {
	case EvCreated:
		if rv == nil {
			return fmt.Errorf("时刻 %d 无生效规则版本: %w", ev.Time, ErrUndefinedTargetType)
		}
		if _, ok := rv.Types[ev.TypeID]; !ok {
			return fmt.Errorf("类型 %q 在时刻 %d 未定义: %w", ev.TypeID, ev.Time, ErrUndefinedTargetType)
		}
		m.typ = ev.TypeID
		m.props = make(map[string]Value)
		note = "created as " + ev.TypeID
	case EvPropertySet:
		if m.typ == "" || rv == nil {
			note = "ignored: 无当前类型"
			break
		}
		r, ok := rv.EffectiveRange(m.typ, ev.Prop)
		if !ok || !r.Contains(ev.Val) {
			if strict {
				return fmt.Errorf("属性 %q 取值 %v 超出 %q 在时刻 %d 的有效范围: %w",
					ev.Prop, ev.Val, m.typ, ev.Time, ErrOutOfRange)
			}
			note = "ignored: 取值越界或属性未定义"
			break
		}
		m.props[ev.Prop] = ev.Val
		note = fmt.Sprintf("set %s", ev.Prop)
	case EvTypeEvolve:
		if rv == nil {
			return fmt.Errorf("时刻 %d 无生效规则版本: %w", ev.Time, ErrUndefinedTargetType)
		}
		if _, ok := rv.Types[ev.TypeID]; !ok {
			return fmt.Errorf("类型 %q 在时刻 %d 未定义: %w", ev.TypeID, ev.Time, ErrUndefinedTargetType)
		}
		if ev.TypeID == m.typ {
			note = "no-op: 同型演变"
			break
		}
		if m.typ == "" || (!rv.IsAncestor(ev.TypeID, m.typ) && !rv.IsAncestor(m.typ, ev.TypeID)) {
			return fmt.Errorf("不允许从 %q 演变为 %q: %w", m.typ, ev.TypeID, ErrInvalidEvolutionPath)
		}
		m.typ = ev.TypeID
		dropped := []string{}
		for p, v := range m.props {
			r, ok := rv.EffectiveRange(m.typ, p)
			if !ok || !r.Contains(v) {
				delete(m.props, p)
				dropped = append(dropped, p)
			}
		}
		sort.Strings(dropped)
		note = fmt.Sprintf("evolved to %s, dropped %v", ev.TypeID, dropped)
	}
	return nil
}
