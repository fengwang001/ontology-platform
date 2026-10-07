package linkrepair

import "sort"

// Arbiter 负责去重与基数约束层面的冲突裁决（优先级第 3、4 级）。
//
// 它只接收已经通过结构判定与引用核对的候选链接，因此：
//   - 重复判定（优先级第 4 级）与基数冲突判定（优先级第 3 级）
//     作用域互不交叠，每条被舍弃记录只有唯一原因；
//   - 不同链接类型的候选天然按 Type 分桶，互不影响；
//   - 同一链接类型的 From 端、To 端约束分别独立计算允许集合，
//     最终保留集合是两个允许集合的交集，因此处理顺序不影响结果。
//
// 固定优先规则（不允许任意选择）：
//  1. 重复键 (Type,From,To) 上保留来源 Offset 最小的一条；
//  2. 基数超限时，保留“对方实例标识字典序最小”的前 cap 个不同对象，
//     对方标识相同的候选之间以最小 Offset 决胜；
//  3. Offset 只是原始快照位置，快照不变则结果不变，重复执行不漂移。
type Arbiter struct {
	types map[LinkTypeID]LinkType
}

// NewArbiter 基于链接类型注册表构造裁决器。
func NewArbiter(types map[LinkTypeID]LinkType) *Arbiter {
	return &Arbiter{types: types}
}

// AdjudicateStats 描述冲突局部范围的工作量度量。
type AdjudicateStats struct {
	ConflictGroups    int // 真正超限、触发裁决排序的分组数
	ConflictGroupWork int // 这些分组内候选记录总数；无冲突分组不计入
	DuplicateGroups   int // 判重命中次数
}

// linkKey 是“内容完全相同”的判重键。
type linkKey struct {
	Type LinkTypeID
	From ObjectID
	To   ObjectID
}

// member 是一个基数分组内的一条候选。
type member struct {
	key    linkKey
	peer   ObjectID // 对方实例（From 端约束下为 To，反之）
	offset int      // 该键的最小来源 Offset
}

// group 收集 (链接类型, 受约束端对象) 下的全部候选。
type group struct {
	typeID   LinkTypeID
	owner    ObjectID
	fromSide bool // true 表示约束 MaxFrom（From 为受约束端）
	members  []member
}

func (g *group) keyString() string {
	side := "to"
	if g.fromSide {
		side = "from"
	}
	return string(g.typeID) + "|" + side + "|" + string(g.owner)
}

// loserInfo 描述一条链接在某个方向输掉竞争时的可复核信息。
type loserInfo struct {
	groupKey      string
	winnerOffsets []int
}

// Adjudicate 在结构有效、引用可用的候选链接上执行去重与基数裁决。
// 输入候选应按 Offset 升序给出；输出 Kept 按 (Type,From,To) 确定性排序。
func (a *Arbiter) Adjudicate(candidates []ScoredLink) ([]Link, []DroppedRecord, AdjudicateStats) {
	dropped := []DroppedRecord{}
	stats := AdjudicateStats{}

	// 第 4 级：精确去重。同一内容只保留 Offset 最小的一条。
	// 候选已按 Offset 升序到达，首次出现即最小 Offset。
	uniqueByKey := make(map[linkKey]ScoredLink, len(candidates))
	order := make([]linkKey, 0, len(candidates))
	for _, cand := range candidates {
		k := linkKey{cand.Type, cand.From, cand.To}
		if _, seen := uniqueByKey[k]; seen {
			kept := uniqueByKey[k]
			dropped = append(dropped, DroppedRecord{
				Record:      rawOf(cand),
				Reason:      ReasonDuplicate,
				Detail:      "identical record already kept at smaller snapshot offset",
				GroupKey:    string(k.Type) + "|" + string(k.From) + "->" + string(k.To),
				KeptOffsets: []int{kept.Offset},
			})
			stats.DuplicateGroups++
			continue
		}
		uniqueByKey[k] = cand
		order = append(order, k)
	}

	// 注册两个方向的基数分组。每条去重后链接仅做 O(1) 登记。
	fromGroups := map[LinkTypeID]map[ObjectID]*group{}
	toGroups := map[LinkTypeID]map[ObjectID]*group{}
	for _, k := range order {
		cand := uniqueByKey[k]
		registerGroup(fromGroups, k, cand.Offset, k.To, true)
		registerGroup(toGroups, k, cand.Offset, k.From, false)
	}

	// 两个方向独立计算允许集合；只对真正超限的分组做排序。
	allowedFrom, fromLosers := a.resolveSide(fromGroups, true, &stats)
	allowedTo, toLosers := a.resolveSide(toGroups, false, &stats)

	// 交集：链接必须同时满足两端约束才保留。一条链接即使两个方向
	// 都输掉，也只登记一条舍弃记录（原因唯一、可区分）。
	kept := []Link{}
	sortedKeys := append([]linkKey(nil), order...)
	sort.Slice(sortedKeys, func(i, j int) bool { return keyLess(sortedKeys[i], sortedKeys[j]) })
	for _, k := range sortedKeys {
		if allowedFrom[k] && allowedTo[k] {
			kept = append(kept, Link{Type: k.Type, From: k.From, To: k.To})
			continue
		}
		groupKeys := []string{}
		winnerOffsets := []int{}
		if lg, lost := fromLosers[k]; lost {
			groupKeys = append(groupKeys, lg.groupKey)
			winnerOffsets = append(winnerOffsets, lg.winnerOffsets...)
		}
		if lg, lost := toLosers[k]; lost {
			groupKeys = append(groupKeys, lg.groupKey)
			winnerOffsets = append(winnerOffsets, lg.winnerOffsets...)
		}
		dropped = append(dropped, DroppedRecord{
			Record:      rawOf(uniqueByKey[k]),
			Reason:      ReasonCardinality,
			Detail:      "lost fixed-priority cardinality selection (smallest peer id, then smallest offset)",
			GroupKey:    joinKeys(groupKeys),
			KeptOffsets: sortedUniqueOffsets(winnerOffsets),
		})
	}

	sort.Slice(dropped, func(i, j int) bool {
		return dropped[i].Record.Offset < dropped[j].Record.Offset
	})
	return kept, dropped, stats
}

func registerGroup(
	buckets map[LinkTypeID]map[ObjectID]*group,
	k linkKey, offset int, peer ObjectID, fromSide bool,
) {
	owner := k.To
	if fromSide {
		owner = k.From
	}
	bucket, ok := buckets[k.Type]
	if !ok {
		bucket = map[ObjectID]*group{}
		buckets[k.Type] = bucket
	}
	g, ok := bucket[owner]
	if !ok {
		g = &group{typeID: k.Type, owner: owner, fromSide: fromSide}
		bucket[owner] = g
	}
	g.members = append(g.members, member{key: k, peer: peer, offset: offset})
}

// resolveSide 计算一个方向上各链接键是否被允许，并返回输掉者信息。
func (a *Arbiter) resolveSide(
	buckets map[LinkTypeID]map[ObjectID]*group,
	fromSide bool, stats *AdjudicateStats,
) (map[linkKey]bool, map[linkKey]loserInfo) {
	allowed := make(map[linkKey]bool)
	losers := map[linkKey]loserInfo{}

	for _, bucket := range buckets {
		for _, g := range bucket {
			cap := a.capFor(g.typeID, fromSide)
			// 无上限，或未超限：无冲突局部，仅线性登记，不排序。
			if cap == Unbounded || len(g.members) <= cap {
				for _, m := range g.members {
					allowed[m.key] = true
				}
				continue
			}

			// 真正超限：只为该局部范围支付排序成本。
			stats.ConflictGroups++
			stats.ConflictGroupWork += len(g.members)

			members := append([]member(nil), g.members...)
			sort.Slice(members, func(i, j int) bool {
				if members[i].peer != members[j].peer {
					return members[i].peer < members[j].peer
				}
				return members[i].offset < members[j].offset
			})

			winnerOffsets := make([]int, 0, cap)
			for _, m := range members[:cap] {
				allowed[m.key] = true
				winnerOffsets = append(winnerOffsets, m.offset)
			}
			info := loserInfo{groupKey: g.keyString(), winnerOffsets: winnerOffsets}
			for _, m := range members[cap:] {
				allowed[m.key] = false
				losers[m.key] = info
			}
		}
	}
	return allowed, losers
}

func (a *Arbiter) capFor(typeID LinkTypeID, fromSide bool) int {
	lt := a.types[typeID]
	if fromSide {
		return lt.Cardinality.MaxFrom
	}
	return lt.Cardinality.MaxTo
}

func keyLess(a, b linkKey) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	if a.From != b.From {
		return a.From < b.From
	}
	return a.To < b.To
}

func rawOf(cand ScoredLink) RawRecord {
	return RawRecord{Offset: cand.Offset, Type: cand.Type, From: cand.From, To: cand.To}
}

func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ";"
		}
		out += k
	}
	return out
}

func sortedUniqueOffsets(in []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}
