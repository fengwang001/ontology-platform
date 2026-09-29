package scd

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// DefaultMaxPointsPerKey 是每个键变更点数的默认上限。
const DefaultMaxPointsPerKey = 100_000

// History 维护多个键的维度取值变更点，并增量生成历史区间。
// 所有方法均可被多个执行体并发调用；读取与提交可以并发进行。
type History struct {
	maxPoints int
	seq       atomic.Int64

	keysMu sync.RWMutex
	keys   map[string]*keyState
}

type keyState struct {
	mu        sync.RWMutex
	points    map[int64]ChangePoint
	order     []int64
	intervals []Interval
}

// Option 配置 History 的可选参数。
type Option func(*History)

// WithMaxPointsPerKey 设置每个键允许保留的变更点数上限（必须 > 0）。
func WithMaxPointsPerKey(n int) Option {
	return func(h *History) {
		if n > 0 {
			h.maxPoints = n
		}
	}
}

// NewHistory 创建一个历史维护器。
func NewHistory(opts ...Option) *History {
	h := &History{maxPoints: DefaultMaxPointsPerKey, keys: make(map[string]*keyState)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// normalizedCommit 是整批事件校验后、进入加锁阶段前的确定性计划。
type normalizedCommit struct {
	// perKey 按键聚合，键保持首次出现顺序；planAt 按事件下标升序。
	byKey map[string][]plannedPoint
}

type plannedPoint struct {
	index int
	at    int64
	event Event
}

// Commit 整批提交变更事件。
//
// 提交是原子的：先在不触碰任何状态的前提下校验全部事件，再按事件下标顺序
// 应用。任何一类非法输入都会整批拒绝，拒绝后变更点与历史保持提交前状态，
// 失败不留痕。同一键同一生效时间的多个事件只保留批内最后一条。
func (h *History) Commit(events []Event) error {
	if h == nil {
		return &CommitError{Reasons: []RejectReason{{
			Code: CodeInvalidArgument, Index: -1,
			Message: "history receiver is nil",
		}}}
	}
	plan, reasons := normalize(events)
	if len(reasons) > 0 {
		return &CommitError{Reasons: reasons}
	}

	// 收集受影响的键并按字典序加锁，杜绝多键交叉加锁造成的死锁。
	affectedKeys := make([]string, 0, len(plan.byKey))
	for key := range plan.byKey {
		affectedKeys = append(affectedKeys, key)
	}
	sort.Strings(affectedKeys)

	states := make(map[string]*keyState, len(affectedKeys))
	var created []string

	for _, key := range affectedKeys {
		h.keysMu.RLock()
		st, ok := h.keys[key]
		h.keysMu.RUnlock()
		if !ok {
			h.keysMu.Lock()
			// 双重检查：两个提交可能同时为同一个新键竞争。
			st, ok = h.keys[key]
			if !ok {
				st = &keyState{points: make(map[int64]ChangePoint)}
				h.keys[key] = st
				created = append(created, key)
			}
			h.keysMu.Unlock()
		}
		st.mu.Lock()
		states[key] = st
	}

	// 第二阶段：在持锁状态下复核变更点数上限（第一阶段无法看到当前状态）。
	for _, key := range affectedKeys {
		st := states[key]
		added := 0
		for _, pp := range plan.byKey[key] {
			if _, exists := st.points[pp.at]; !exists {
				added++
			}
		}
		if len(st.order)+added > h.maxPoints {
			reasons = append(reasons, RejectReason{
				Code:    CodeTooManyChanges,
				Index:   plan.byKey[key][0].index,
				Key:     key,
				Message: fmt.Sprintf("change points for key would be %d, exceeding limit %d", len(st.order)+added, h.maxPoints),
			})
		}
	}
	if len(reasons) > 0 {
		sort.SliceStable(reasons, func(i, j int) bool { return reasons[i].Index < reasons[j].Index })
		// 仍持有全部受影响键的写锁：新建状态尚无变更点，先从索引中摘除，
		// 使被拒提交与“键从未出现”完全等价（失败不留痕）。
		if len(created) > 0 {
			h.keysMu.Lock()
			for _, key := range created {
				delete(h.keys, key)
			}
			h.keysMu.Unlock()
		}
		for _, key := range affectedKeys {
			states[key].mu.Unlock()
		}
		return &CommitError{Reasons: reasons}
	}

	// 应用阶段：按事件原始下标顺序逐条做增量维护，Seq 在持键锁期间分配，
	// 因此同一键严格按到达顺序递增。
	for _, key := range affectedKeys {
		st := states[key]
		for _, pp := range plan.byKey[key] {
			seq := h.seq.Add(1)
			st.apply(key, pp, seq)
		}
	}
	for _, key := range affectedKeys {
		states[key].mu.Unlock()
	}
	return nil
}

// normalize 在不读取也不修改任何状态的前提下做整批静态校验与按键聚合。
// 同一键同一 At 的多条事件只保留批内最后一条（下标最大者为“后到者”）。
func normalize(events []Event) (normalizedCommit, []RejectReason) {
	plan := normalizedCommit{byKey: make(map[string][]plannedPoint)}
	if len(events) == 0 {
		return plan, []RejectReason{{
			Code: CodeEmptyBatch, Index: -1,
			Message: "event batch is empty; nothing to commit",
		}}
	}

	var reasons []RejectReason
	for i, ev := range events {
		if ev.Key == "" {
			reasons = append(reasons, RejectReason{
				Code: CodeEmptyKey, Index: i, At: ev.At,
				Message: "event key must not be empty",
			})
		}
		if ev.Op != OpUpdate && ev.Op != OpDelete {
			reasons = append(reasons, RejectReason{
				Code: CodeInvalidOp, Index: i, Key: ev.Key, At: ev.At,
				Message: fmt.Sprintf("op must be OpUpdate(%d) or OpDelete(%d), got %d", OpUpdate, OpDelete, ev.Op),
			})
		}
		if ev.At < MinTime || ev.At > MaxTime {
			reasons = append(reasons, RejectReason{
				Code: CodeTimeOutOfRange, Index: i, Key: ev.Key, At: ev.At,
				Message: fmt.Sprintf("effective time %d out of bounds [%d,%d]", ev.At, MinTime, MaxTime),
			})
		}
	}
	if len(reasons) > 0 {
		return normalizedCommit{byKey: map[string][]plannedPoint{}}, reasons
	}

	// lastIndex[key][at] 记录该键该生效时间批内最后一条事件在聚合切片中的位置。
	lastIndex := make(map[string]map[int64]int)
	for i, ev := range events {
		slot, ok := lastIndex[ev.Key]
		if !ok {
			slot = make(map[int64]int)
			lastIndex[ev.Key] = slot
		}
		if pos, exists := slot[ev.At]; exists {
			// 同一生效时间只保留后到者：就地替换，聚合顺序仍由下标决定。
			plan.byKey[ev.Key][pos] = plannedPoint{index: i, at: ev.At, event: ev}
		} else {
			slot[ev.At] = len(plan.byKey[ev.Key])
			plan.byKey[ev.Key] = append(plan.byKey[ev.Key], plannedPoint{index: i, at: ev.At, event: ev})
		}
	}
	for key := range plan.byKey {
		sort.SliceStable(plan.byKey[key], func(i, j int) bool {
			return plan.byKey[key][i].index < plan.byKey[key][j].index
		})
	}
	return plan, nil
}

// apply 对单个键做一次增量变更点维护，并增量修补受影响的历史区间。
// 调用方必须持有该键的写锁。
func (st *keyState) apply(key string, pp plannedPoint, seq int64) {
	ev := pp.event
	pos, exists := indexOf(st.order, pp.at)

	if !exists {
		// 新生效时间：插入有序变更点。
		st.order = append(st.order, 0)
		copy(st.order[pos+1:], st.order[pos:])
		st.order[pos] = pp.at
	}
	st.points[pp.at] = ChangePoint{At: ev.At, Op: ev.Op, Value: ev.Value, Seq: seq}

	st.repairIntervalsFrom(key, pos)
}

// repairIntervalsFrom 从变更点位置 pos 起增量重建后续区间。
// pos 之前的区间行不可能被本次变更影响（其右端点若落在 pos 处会就地更新）。
// 调用方必须持有写锁。
func (st *keyState) repairIntervalsFrom(key string, pos int) {
	// 找到首个起点下标 >= order[pos] 的历史行。
	first := sort.Search(len(st.intervals), func(i int) bool {
		return st.intervals[i].Start >= st.order[pos]
	})

	// 前一行（若存在）原本可能延伸到 order[pos] 之后；仅当新点落在其内部时
	// 才把右端点收敛到 order[pos]，完成“拆分该行”。若前一行早已被中间的
	// 删除点闭合（End <= order[pos]），则保持不动。
	if first > 0 && st.intervals[first-1].End > st.order[pos] {
		st.intervals[first-1].End = st.order[pos]
	}

	// 丢弃从 first 起的旧行，改为由变更点集合确定性重生成。
	st.intervals = st.intervals[:first]
	for i := pos; i < len(st.order); i++ {
		point := st.points[st.order[i]]
		if point.Op == OpDelete {
			continue // 删除点不产生区间。
		}
		end := MaxTime
		if i+1 < len(st.order) {
			end = st.order[i+1]
		}
		// 相邻两行即使值相同也不合并：更新点各自产生自己的区间。
		st.intervals = append(st.intervals, Interval{
			Key: key, Start: point.At, End: end, Value: point.Value,
		})
	}
}

// indexOf 返回有序变更点中 at 的位置；不存在时返回应当插入的位置与 false。
func indexOf(order []int64, at int64) (int, bool) {
	pos := sort.Search(len(order), func(i int) bool { return order[i] >= at })
	if pos < len(order) && order[pos] == at {
		return pos, true
	}
	return pos, false
}

// Intervals 返回某个键当前历史区间的快照，按起点升序。
// 返回的切片为拷贝，调用方可自由修改；读取与提交可以并发。
func (h *History) Intervals(key string) []Interval {
	if h == nil {
		return nil
	}
	h.keysMu.RLock()
	st, ok := h.keys[key]
	h.keysMu.RUnlock()
	if !ok {
		return []Interval{}
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return cloneIntervals(st.intervals)
}

// ValueAt 查询某个键在指定时间点命中的取值；未命中时 ok 为 false。
// 任意时间点至多命中一行：区间为左闭右开，删除点之后到下一个更新点之间无取值。
func (h *History) ValueAt(key string, at int64) (value string, ok bool) {
	if h == nil {
		return "", false
	}
	h.keysMu.RLock()
	st, exists := h.keys[key]
	h.keysMu.RUnlock()
	if !exists {
		return "", false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	ivs := st.intervals
	// 最后一个 Start <= at 的行才可能包含 at（行的 End 由下一个变更点决定）。
	idx := sort.Search(len(ivs), func(i int) bool { return ivs[i].Start > at }) - 1
	if idx < 0 || at >= ivs[idx].End {
		return "", false
	}
	return ivs[idx].Value, true
}

// Snapshot 返回全部键历史区间的快照；同一键内行升序，键之间按字典序。
// 每个键各自取一致快照，不同键之间不要求全局同一时刻，但每个键内部良构。
func (h *History) Snapshot() []Interval {
	if h == nil {
		return []Interval{}
	}
	h.keysMu.RLock()
	keys := make([]string, 0, len(h.keys))
	states := make([]*keyState, 0, len(h.keys))
	for key, st := range h.keys {
		keys = append(keys, key)
		states = append(states, st)
	}
	h.keysMu.RUnlock()
	order := make([]int, len(keys))
	for i := range keys {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })

	var out []Interval
	for _, oi := range order {
		st := states[oi]
		st.mu.RLock()
		out = append(out, cloneIntervals(st.intervals)...)
		st.mu.RUnlock()
	}
	return out
}

// Keys 返回当前存在变更点的全部键，按字典序。
func (h *History) Keys() []string {
	if h == nil {
		return []string{}
	}
	h.keysMu.RLock()
	keys := make([]string, 0, len(h.keys))
	for key := range h.keys {
		keys = append(keys, key)
	}
	h.keysMu.RUnlock()
	sort.Strings(keys)
	return keys
}

// ChangePointCount 返回某个键当前保留的变更点数。
func (h *History) ChangePointCount(key string) int {
	if h == nil {
		return 0
	}
	h.keysMu.RLock()
	st, ok := h.keys[key]
	h.keysMu.RUnlock()
	if !ok {
		return 0
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.order)
}

// SelfCheck 校验全部键的区间良构性，并与由变更点重新生成的结果逐行比对。
// 检查项：起点严格升序、任意两行不重叠、左闭右开边界一致、任意点至多命中一行、
// 增量维护结果与纯重算结果完全一致。
func (h *History) SelfCheck() error {
	if h == nil {
		return &CommitError{Reasons: []RejectReason{{
			Code: CodeSelfCheck, Index: -1, Message: "history receiver is nil",
		}}}
	}
	h.keysMu.RLock()
	snapshot := make(map[string]*keyState, len(h.keys))
	for key, st := range h.keys {
		snapshot[key] = st
	}
	h.keysMu.RUnlock()

	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		st := snapshot[key]
		st.mu.RLock()
		points := make(map[int64]ChangePoint, len(st.points))
		for at, p := range st.points {
			points[at] = p
		}
		order := append([]int64(nil), st.order...)
		got := cloneIntervals(st.intervals)
		st.mu.RUnlock()

		want := buildIntervals(key, order, points)
		if err := checkWellFormed(key, order, points, got); err != nil {
			return err
		}
		if !intervalsEqual(want, got) {
			return &CommitError{Reasons: []RejectReason{{
				Code: CodeSelfCheck, Key: key,
				Message: fmt.Sprintf("incremental intervals %v differ from recomputed %v", got, want),
			}}}
		}
	}
	return nil
}

// checkWellFormed 校验单个键的区间良构性与点查询唯一性。
func checkWellFormed(key string, order []int64, points map[int64]ChangePoint, ivs []Interval) error {
	reason := func(format string, args ...any) error {
		return &CommitError{Reasons: []RejectReason{{
			Code: CodeSelfCheck, Key: key,
			Message: fmt.Sprintf(format, args...),
		}}}
	}
	if len(order) != len(points) {
		return reason("order/points size mismatch: %d order vs %d points", len(order), len(points))
	}
	for i, at := range order {
		if i > 0 && order[i-1] >= at {
			return reason("change points not strictly ascending at index %d: %d >= %d", i, order[i-1], at)
		}
		if _, ok := points[at]; !ok {
			return reason("order entry %d missing in points map", at)
		}
	}
	for i, iv := range ivs {
		if iv.Key != key {
			return reason("interval %d key label %q does not match %q", i, iv.Key, key)
		}
		if iv.Start >= iv.End {
			return reason("interval %d empty or reversed: [%d,%d)", i, iv.Start, iv.End)
		}
		if i > 0 {
			prev := ivs[i-1]
			if iv.Start < prev.End {
				return reason("intervals %d and %d overlap: [%d,%d) vs [%d,%d)",
					i-1, i, prev.Start, prev.End, iv.Start, iv.End)
			}
			if iv.Start <= prev.Start {
				return reason("intervals not strictly ascending at %d", i)
			}
		}
		// 区间起点必须恰好是一个更新变更点，右端点必须是下一个变更点或 MaxTime。
		p, ok := points[iv.Start]
		if !ok || p.Op != OpUpdate || p.Value != iv.Value {
			return reason("interval %d start %d is not a matching update point", i, iv.Start)
		}
		pi, _ := indexOf(order, iv.Start)
		wantEnd := MaxTime
		if pi+1 < len(order) {
			wantEnd = order[pi+1]
		}
		if iv.End != wantEnd {
			return reason("interval %d end %d != next change point %d", i, iv.End, wantEnd)
		}
	}
	return nil
}

// cloneIntervals 返回区间切片的深拷贝，供快照对外发布。
func cloneIntervals(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return []Interval{}
	}
	out := make([]Interval, len(ivs))
	copy(out, ivs)
	return out
}

func intervalsEqual(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildIntervals 由变更点集合确定性生成历史区间：仅更新点产生左闭右开区间，
// 右端点为下一个变更点时间，末行到 MaxTime；相邻同值不合并。
func buildIntervals(key string, order []int64, points map[int64]ChangePoint) []Interval {
	out := []Interval{}
	for i, at := range order {
		p := points[at]
		if p.Op != OpUpdate {
			continue
		}
		end := MaxTime
		if i+1 < len(order) {
			end = order[i+1]
		}
		out = append(out, Interval{Key: key, Start: at, End: end, Value: p.Value})
	}
	return out
}
