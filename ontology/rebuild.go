package ontology

import (
	"errors"
	"maps"
	"sort"
	"sync"
)

// 重建错误分类。同一次重建请求同时具备多类错误条件时，
// 按下列声明顺序（优先级从高到低）只报告其中一类：
//  1. ErrCutoffBeforeFirstEvent  请求层面：截止时刻早于首个事件
//  2. ErrAmbiguousOrder          流完整性：存在无法确定先后的并列记录
//  3. ErrUndefinedTargetType     语义：目标类型在当时版本中未定义
//  4. ErrInvalidEvolutionPath    语义：演变路径与当时继承关系矛盾
var (
	ErrCutoffBeforeFirstEvent = errors.New("ontology: cutoff before first event")
	ErrAmbiguousOrder         = errors.New("ontology: ambiguous event order")
	ErrUndefinedTargetType    = errors.New("ontology: undefined target type")
	ErrInvalidEvolutionPath   = errors.New("ontology: invalid evolution path")
)

// State 是重建得到的实例状态。
type State struct {
	TypeID string
	// Values 是当前生效类型下可见的属性取值。
	Values map[string]Value
	// Suppressed 是历史赋值但在当前生效类型下不再有意义
	// （属性未定义或取值越界）而被遮蔽的属性及其原值。
	Suppressed map[string]Value
}

// RebuildStats 记录一次重建的开销，用于独立验证
// 重建成本不随类型演变事件总数线性增长。
type RebuildStats struct {
	EventsScanned   int
	CheckpointUsed  bool
	CheckpointCount int
}

// checkpoint 是重建中间状态的快照。
type checkpoint struct {
	Time        int64
	Count       int
	LastSeq     uint64
	TypeID      string
	Values      map[string]Value
	SchemaCount int
}

// Rebuilder 基于检查点加速状态重建。
type Rebuilder struct {
	store  *EventStore
	schema *SchemaRegistry
	every  int

	mu          sync.Mutex
	checkpoints map[string][]checkpoint
	// modSeq 镜像每个实例的追加次数，由追加回调维护，
	// 用于在不持有存储锁的情况下判断快照期间是否有新追加。
	modSeq map[string]uint64
}

// NewRebuilder 创建重建器。every 为检查点间隔（每处理 every 个
// 事件落一次快照），必须为正数。
func NewRebuilder(store *EventStore, schema *SchemaRegistry, every int) *Rebuilder {
	if every <= 0 {
		every = 64
	}
	rb := &Rebuilder{
		store:       store,
		schema:      schema,
		every:       every,
		checkpoints: make(map[string][]checkpoint),
		modSeq:      make(map[string]uint64),
	}
	store.SetAppendHook(rb.invalidate)
	return rb
}

// invalidate 在事件追加后丢弃被新事件（按逻辑时刻）穿越的检查点，
// 并递增该实例的修改计数。
func (rb *Rebuilder) invalidate(ev Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.modSeq[ev.InstanceID]++
	cps := rb.checkpoints[ev.InstanceID]
	keep := cps[:0]
	for _, cp := range cps {
		if cp.Time < ev.Time {
			keep = append(keep, cp)
		}
	}
	rb.checkpoints[ev.InstanceID] = keep
}

// Rebuild 重建实例在截止时刻 cutoff（含）的状态。
// 重建只读：失败或成功都不会写入事件流。
//
// 开销模型：设距上次重建新追加的事件数为 m、检查点间隔为 K，
// 单次重建开销为 O(log n + m + K)，与实例累计的类型演变事件
// 总数 n 无关（仅含对数因子）。RebuildStats.EventsScanned 可
// 用于独立验证实际重放的事件数不超过 m+K。
func (rb *Rebuilder) Rebuild(instanceID string, cutoff int64) (State, RebuildStats, error) {
	var stats RebuildStats
	schemaCount := rb.schema.VersionCount()

	// 阶段 1：在存储读锁内选择检查点并拷贝重放窗口，
	// 保证检查点与窗口来自同一份一致快照。
	var window []Event
	var cp checkpoint
	haveCp := false
	var c0 uint64
	var hi int
	rb.store.ReadWindow(instanceID, func(sorted []Event) {
		hi = sort.Search(len(sorted), func(i int) bool { return sorted[i].Time > cutoff })
		rb.mu.Lock()
		defer rb.mu.Unlock()
		c0 = rb.modSeq[instanceID]
		cps := rb.checkpoints[instanceID]
		lo := 0
		for i := len(cps) - 1; i >= 0; i-- {
			c := cps[i]
			if c.Time <= cutoff && c.SchemaCount == schemaCount &&
				c.Count <= hi && (c.Count == 0 || sorted[c.Count-1].Seq == c.LastSeq) {
				cp, haveCp, lo = c, true, c.Count
				break
			}
		}
		window = append(window, sorted[lo:hi]...)
	})

	// 优先级 1：截止时刻早于首个事件。
	if hi == 0 {
		return State{}, stats, ErrCutoffBeforeFirstEvent
	}
	// 优先级 2：并列记录。检查点覆盖的前缀在建点时已通过检查；
	// 逻辑时刻 <= 检查点时刻的新事件会令其失效，故只需检查窗口内。
	for i := 1; i < len(window); i++ {
		if window[i].Time == window[i-1].Time {
			return State{}, stats, ErrAmbiguousOrder
		}
	}

	start := 0
	current := ""
	st := State{Values: map[string]Value{}, Suppressed: map[string]Value{}}
	if haveCp {
		start = cp.Count
		current = cp.TypeID
		maps.Copy(st.Values, cp.Values)
		stats.CheckpointUsed = true
	}

	var newCps []checkpoint
	for i, ev := range window {
		v := rb.schema.VersionAt(ev.Time)
		switch ev.Kind {
		case EventCreate:
			if _, ok := v.Types[ev.TypeID]; !ok {
				return State{}, stats, ErrUndefinedTargetType
			}
			if current == "" {
				current = ev.TypeID
			}
		case EventSetProperty:
			if current != "" {
				if rng, ok := Resolve(v, current, ev.Property); ok && rng.Contains(ev.Value) {
					st.Values[ev.Property] = ev.Value
				}
			}
		case EventEvolveType:
			if _, ok := v.Types[ev.TypeID]; !ok {
				return State{}, stats, ErrUndefinedTargetType
			}
			if current == "" {
				return State{}, stats, ErrInvalidEvolutionPath
			}
			if ev.TypeID != v.Types[current].Parent && !IsDirectChild(v, current, ev.TypeID) {
				return State{}, stats, ErrInvalidEvolutionPath
			}
			current = ev.TypeID
		}
		stats.EventsScanned++
		if total := start + i + 1; total%rb.every == 0 {
			newCps = append(newCps, checkpoint{
				Time:        ev.Time,
				Count:       total,
				LastSeq:     ev.Seq,
				TypeID:      current,
				Values:      maps.Clone(st.Values),
				SchemaCount: schemaCount,
			})
		}
	}

	// 截止时刻遮蔽：按 cutoff 生效的规则版本与最终类型，
	// 把不再有意义或取值越界的历史赋值移入 Suppressed。
	vc := rb.schema.VersionAt(cutoff)
	for prop, val := range st.Values {
		rng, ok := Resolve(vc, current, prop)
		if !ok || !rng.Contains(val) {
			st.Suppressed[prop] = val
			delete(st.Values, prop)
		}
	}
	st.TypeID = current

	// 仅当快照读取后该实例没有新追加、且规则版本未变化时，
	// 才把本次产生的检查点合并回去，避免留下过期快照。
	if len(newCps) > 0 {
		rb.mu.Lock()
		if rb.modSeq[instanceID] == c0 && rb.schema.VersionCount() == schemaCount {
			existing := rb.checkpoints[instanceID]
			for _, cp := range newCps {
				dup := false
				for _, e := range existing {
					if e.Count == cp.Count {
						dup = true
						break
					}
				}
				if !dup {
					existing = append(existing, cp)
				}
			}
			sort.Slice(existing, func(i, j int) bool { return existing[i].Count < existing[j].Count })
			rb.checkpoints[instanceID] = existing
		}
		rb.mu.Unlock()
	}
	rb.mu.Lock()
	stats.CheckpointCount = len(rb.checkpoints[instanceID])
	rb.mu.Unlock()
	return st, stats, nil
}

// canonical 返回实例中 Time <= cutoff 的事件，按逻辑时刻排序，
// 并执行请求层面与流完整性层面的错误检查（优先级 1、2）。
func canonical(events []Event, cutoff int64) ([]Event, error) {
	var eligible []Event
	for _, ev := range events {
		if ev.Time <= cutoff {
			eligible = append(eligible, ev)
		}
	}
	if len(eligible) == 0 {
		return nil, ErrCutoffBeforeFirstEvent
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].Time < eligible[j].Time
	})
	for i := 1; i < len(eligible); i++ {
		if eligible[i].Time == eligible[i-1].Time {
			return nil, ErrAmbiguousOrder
		}
	}
	return eligible, nil
}
