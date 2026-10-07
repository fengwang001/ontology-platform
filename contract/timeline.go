package contract

import (
	"math"
	"sort"
	"sync/atomic"
)

// queryComparisons 统计有效值查询中二分查找的比较次数，
// 仅供测试以可验证方式证明查询开销与合同累计协议总数无关。
var queryComparisons int64

func resetQueryComparisons() { atomic.StoreInt64(&queryComparisons, 0) }

func getQueryComparisons() int64 { return atomic.LoadInt64(&queryComparisons) }

// segment 是某条款有效值时间线上的一段：[start, 下一段.start) 内取值恒为 value。
type segment struct {
	start  int
	value  int
	source int // 来源协议编号，-1 表示主合同
}

// clauseTimeline 是单个条款的物化有效值时间线。
// 只记录触及本条款的协议，因此查询开销与合同累计协议总数无关。
type clauseTimeline struct {
	clause    int
	segments  []segment // 按 start 升序，首段 start 为 math.MinInt（主合同原值）
	touching  []int     // 修改过本条款的协议编号（含尚未生效的）
	dirty     bool
	dirtyFrom int // [dirtyFrom, +∞) 的段需要重建
}

func newClauseTimeline(clause, masterValue int) *clauseTimeline {
	return &clauseTimeline{
		clause: clause,
		segments: []segment{
			{start: math.MinInt, value: masterValue, source: -1},
		},
	}
}

// markDirty 将 [day, +∞) 标记为待重建。
func (c *Contract) markDirty(clause, day int) {
	ct := c.timelines[clause]
	if ct == nil {
		ct = newClauseTimeline(clause, c.master[clause])
		c.timelines[clause] = ct
	}
	if !ct.dirty || day < ct.dirtyFrom {
		ct.dirty = true
		ct.dirtyFrom = day
	}
}

// valueAt 返回 clause 在 day 的有效值与来源协议编号（-1 为主合同）。
// 开销为 O(log k)，k 为本条款时间线的段数。
func (c *Contract) valueAt(clause, day int) (int, int) {
	ct := c.timelines[clause]
	if ct == nil {
		return c.master[clause], -1
	}
	if ct.dirty {
		c.rebuild(ct)
	}
	i := sort.Search(len(ct.segments), func(i int) bool {
		atomic.AddInt64(&queryComparisons, 1)
		return ct.segments[i].start > day
	}) - 1
	if i < 0 {
		return c.master[clause], -1
	}
	return ct.segments[i].value, ct.segments[i].source
}

// capDay 返回生效日上限：合同终止后，终止日之后的协议一律不生效。
func (c *Contract) capDay() int {
	if c.terminated {
		return c.termDay
	}
	return math.MaxInt
}

// revokedAt 判定协议 aid 在 day 当日是否处于被撤销状态。
// 撤销协议自身也可被撤销（再撤销则恢复），沿撤销链递归；
// 链上每条边都指向更早创建的协议，递归必然终止。
func (c *Contract) revokedAt(aid, day, capDay int, memo map[int]bool) bool {
	if v, ok := memo[aid]; ok {
		return v
	}
	res := false
	for _, rid := range c.revsOf[aid] {
		r := c.amendments[rid]
		if !r.effective || r.effDay > day || r.effDay > capDay {
			continue
		}
		if !c.revokedAt(rid, day, capDay, memo) {
			res = true
			break
		}
	}
	memo[aid] = res
	return res
}

// rebuild 从 ct.dirtyFrom 起重物化该条款的时间线。
// 候选变化日 = 触及本条款的协议的生效日 ∪ 这些协议被撤销状态可能翻转的日子。
func (c *Contract) rebuild(ct *clauseTimeline) {
	from := ct.dirtyFrom
	capDay := c.capDay()

	// 截断 [from, +∞) 的旧段，保留 from 之前仍然有效的部分。
	i := sort.Search(len(ct.segments), func(i int) bool { return ct.segments[i].start >= from })
	ct.segments = ct.segments[:i]
	if len(ct.segments) == 0 {
		ct.segments = append(ct.segments, segment{start: math.MinInt, value: c.master[ct.clause], source: -1})
	}
	cur := ct.segments[len(ct.segments)-1]

	// 收集候选变化日。
	daySet := map[int]struct{}{}
	flipMemo := map[int][]int{}
	var flips func(aid int) []int
	flips = func(aid int) []int {
		if v, ok := flipMemo[aid]; ok {
			return v
		}
		var out []int
		for _, rid := range c.revsOf[aid] {
			r := c.amendments[rid]
			if !r.effective || r.effDay > capDay {
				continue
			}
			out = append(out, r.effDay)
			out = append(out, flips(rid)...)
		}
		flipMemo[aid] = out
		return out
	}
	for _, aid := range ct.touching {
		a := c.amendments[aid]
		if !a.effective || a.effDay > capDay {
			continue
		}
		if a.effDay >= from {
			daySet[a.effDay] = struct{}{}
		}
		for _, d := range flips(aid) {
			if d >= from && d <= capDay {
				daySet[d] = struct{}{}
			}
		}
	}
	days := make([]int, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Ints(days)

	// 逐候选日求胜出协议：生效日最晚；并列取签署完成时刻较后者。
	for _, d := range days {
		revMemo := map[int]bool{}
		bestID := -1
		bestEff := 0
		bestSeq := int64(-1)
		for _, aid := range ct.touching {
			a := c.amendments[aid]
			if !a.effective || a.effDay > d || a.effDay > capDay {
				continue
			}
			if c.revokedAt(aid, d, capDay, revMemo) {
				continue
			}
			if bestID == -1 || a.effDay > bestEff || (a.effDay == bestEff && a.signSeq > bestSeq) {
				bestID, bestEff, bestSeq = aid, a.effDay, a.signSeq
			}
		}
		var seg segment
		if bestID == -1 {
			seg = segment{start: d, value: c.master[ct.clause], source: -1}
		} else {
			seg = segment{start: d, value: c.amendments[bestID].mods[ct.clause], source: bestID}
		}
		if seg.value != cur.value || seg.source != cur.source {
			ct.segments = append(ct.segments, seg)
			cur = seg
		}
	}
	ct.dirty = false
}
