package backupretention

import "sort"

// RetentionReason 描述一个备份“为什么必须保留”，三类原因可同时具备。
type RetentionReason struct {
	// DirectLayers 直接保留：该备份当选为哪些层的周期代表。
	DirectLayers []Layer
	// Dependency 依赖保护：它是某个被保留备份的祖先。
	Dependency bool
	// LegalHold 法律保留：备份自身被设置法律保留。
	LegalHold bool
}

// PlannedBackup 是计划中针对单个备份的判定。
type PlannedBackup struct {
	ID        string
	CreatedAt int64
	Kept      bool
	Reason    RetentionReason
}

// Plan 是某一时刻只读清理计划的完整快照。
// Retained 与 Deletable 均按 (创建时刻, 标识) 升序唯一确定。
type Plan struct {
	Now       int64
	Policy    Policy
	Retained  []PlannedBackup
	Deletable []PlannedBackup
}

// direct 描述一个直接保留备份当选的单个层原因。
type directSeat struct {
	backup *Backup
	layer  Layer
}

// electLayer 选出某一层最近 N 个周期（含当前周期）各自的代表。
// 每个周期内：可恢复备份中创建时刻最晚者当选，时刻相同取标识字典序较大者；
// 该周期没有可恢复备份则无代表。候选只包含创建时刻不晚于 now 的备份。
//
// 只遍历该层视角下的备份一次，时间 O(n)。
func electLayer(reg *registry, layer Layer, count int, now int64, recoverable map[*Backup]bool) []directSeat {
	if count <= 0 {
		return nil
	}
	current := periodOf(layer, now)
	lo := current - int64(count) + 1 // 窗口下界（含）；空周期自然无代表

	type best struct {
		b *Backup
	}
	byPeriod := make(map[int64]*best)
	for _, b := range reg.all() {
		if b.CreatedAt > now || !recoverable[b] {
			continue
		}
		p := periodOf(layer, b.CreatedAt)
		if p < lo || p > current {
			continue
		}
		cur := byPeriod[p]
		if cur == nil {
			byPeriod[p] = &best{b: b}
		} else if b.CreatedAt > cur.b.CreatedAt ||
			(b.CreatedAt == cur.b.CreatedAt && b.ID > cur.b.ID) {
			cur.b = b
		}
	}
	seats := make([]directSeat, 0, len(byPeriod))
	for _, w := range byPeriod {
		seats = append(seats, directSeat{backup: w.b, layer: layer})
	}
	return seats
}

// computePlan 是只读计划的完整线性时间实现：
//  1. O(n+深度) 计算可恢复性；
//  2. 三层各 O(n) 选举直接保留；
//  3. O(n+深度) 将直接保留与法律保留沿父链闭包，标记依赖保护；
//  4. O(n log n) 按 (时刻, 标识) 排序输出。
func computePlan(reg *registry, policy Policy, now int64) *Plan {
	recoverable := computeRecoverable(reg)

	kept := make(map[*Backup]*retainBits, reg.len())
	ensure := func(b *Backup) *retainBits {
		rb, ok := kept[b]
		if !ok {
			rb = &retainBits{}
			kept[b] = rb
		}
		return rb
	}

	// 直接保留（仅可恢复备份可能当选）。
	for _, layer := range [3]Layer{LayerDaily, LayerWeekly, LayerMonthly} {
		for _, seat := range electLayer(reg, layer, policy.count(layer), now, recoverable) {
			ensure(seat.backup).addLayer(seat.layer)
		}
	}

	// 法律保留：不受可恢复性限制，损坏备份也可命中。
	for _, b := range reg.all() {
		if b.LegalHold {
			ensure(b).legal = true
		}
	}

	// 依赖保护：每个保留根的全部祖先都必须保留。
	// 使用全局 visited，使每条父边在整个闭包阶段最多被消费一次。
	visited := make(map[*Backup]bool, reg.len())
	roots := make([]*Backup, 0, len(kept))
	for b := range kept {
		roots = append(roots, b)
	}
	for _, root := range roots {
		cur := root
		for cur.ParentID != "" {
			parent := reg.byID[cur.ParentID]
			ensure(parent).dependency = true
			if visited[parent] {
				break // 该祖先向上的整条链此前已闭包
			}
			visited[parent] = true
			cur = parent
		}
	}

	plan := &Plan{Now: now, Policy: policy}
	for _, b := range reg.all() {
		pb := PlannedBackup{ID: b.ID, CreatedAt: b.CreatedAt}
		if rb, ok := kept[b]; ok {
			pb.Kept = true
			pb.Reason = rb.toReason()
			plan.Retained = append(plan.Retained, pb)
		} else {
			pb.Reason = RetentionReason{DirectLayers: []Layer{}}
			plan.Deletable = append(plan.Deletable, pb)
		}
	}
	sortPlanned(plan.Retained)
	sortPlanned(plan.Deletable)
	return plan
}

// retainBits 是单个备份保留原因的可变内部表示。
type retainBits struct {
	layers     [3]bool
	dependency bool
	legal      bool
}

func (rb *retainBits) addLayer(l Layer) {
	switch l {
	case LayerDaily:
		rb.layers[0] = true
	case LayerWeekly:
		rb.layers[1] = true
	case LayerMonthly:
		rb.layers[2] = true
	}
}

func (rb *retainBits) toReason() RetentionReason {
	r := RetentionReason{Dependency: rb.dependency, LegalHold: rb.legal}
	if rb.layers[0] {
		r.DirectLayers = append(r.DirectLayers, LayerDaily)
	}
	if rb.layers[1] {
		r.DirectLayers = append(r.DirectLayers, LayerWeekly)
	}
	if rb.layers[2] {
		r.DirectLayers = append(r.DirectLayers, LayerMonthly)
	}
	return r
}

func sortPlanned(s []PlannedBackup) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].CreatedAt != s[j].CreatedAt {
			return s[i].CreatedAt < s[j].CreatedAt
		}
		return s[i].ID < s[j].ID
	})
}
