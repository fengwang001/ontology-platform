package staleness

import "sort"

// staleLocked 递归判定已物化分区是否过期：存在输入分区的消费记录与其当前版本
// 不等，或该输入分区自身过期。未物化者为缺失，不算过期。调用方须持有锁。
func (e *Engine) staleLocked(a *asset, part int, memo map[PartitionRef]bool) bool {
	key := PartitionRef{Asset: a.name, Partition: part}
	if v, ok := memo[key]; ok {
		return v
	}
	p := a.peek(part)
	if p == nil || !p.materialized {
		memo[key] = false
		return false
	}
	stale := false
	for upName, consumed := range p.consumed {
		up := e.assets[upName]
		for inPart, consumedVer := range consumed {
			cur := up.peek(inPart)
			if cur == nil || !cur.materialized || cur.version != consumedVer ||
				e.staleLocked(up, inPart, memo) {
				stale = true
				break
			}
		}
		if stale {
			break
		}
	}
	memo[key] = stale
	return stale
}

// IsStale 判定一个已物化分区是否过期；未物化者为缺失，返回 false。
func (e *Engine) IsStale(assetName string, part int) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookup(assetName, part)
	if err != nil {
		return false, err
	}
	return e.staleLocked(a, part, make(map[PartitionRef]bool)), nil
}

// Inspect 返回分区的物化状态、版本与过期判定，便于观测与日志。
func (e *Engine) Inspect(assetName string, part int) (materialized bool, version int, stale bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookup(assetName, part)
	if err != nil {
		return false, 0, false, err
	}
	p := a.peek(part)
	if p == nil {
		return false, 0, false, nil
	}
	return p.materialized, p.version, e.staleLocked(a, part, make(map[PartitionRef]bool)), nil
}

// sortRefs 按（资产层深，资产名，分区号）升序排序。
func (e *Engine) sortRefs(refs []PartitionRef) {
	sort.Slice(refs, func(i, j int) bool {
		di, dj := e.assets[refs[i].Asset].depth, e.assets[refs[j].Asset].depth
		if di != dj {
			return di < dj
		}
		if refs[i].Asset != refs[j].Asset {
			return refs[i].Asset < refs[j].Asset
		}
		return refs[i].Partition < refs[j].Partition
	})
}

// PlanBackfill 给定目标分区，返回需物化的分区：目标本身及其全部传递输入中
// 缺失或过期者，新鲜的一律不进入。按（资产层深，资产名，分区号）升序。
// 任一目标非法时整体拒绝，不改变任何状态。
func (e *Engine) PlanBackfill(targets []PartitionRef) ([]PartitionRef, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range targets {
		if _, err := e.lookup(t.Asset, t.Partition); err != nil {
			return nil, err
		}
	}
	memo := make(map[PartitionRef]bool)
	need := make(map[PartitionRef]struct{})
	visited := make(map[PartitionRef]struct{})
	var walk func(a *asset, part int)
	walk = func(a *asset, part int) {
		key := PartitionRef{Asset: a.name, Partition: part}
		if _, ok := visited[key]; ok {
			return
		}
		visited[key] = struct{}{}
		p := a.peek(part)
		if p == nil || !p.materialized || e.staleLocked(a, part, memo) {
			need[key] = struct{}{}
		}
		for _, ref := range e.inputs(a, part) {
			walk(e.assets[ref.Asset], ref.Partition)
		}
	}
	for _, t := range targets {
		walk(e.assets[t.Asset], t.Partition)
	}
	plan := make([]PartitionRef, 0, len(need))
	for ref := range need {
		plan = append(plan, ref)
	}
	e.sortRefs(plan)
	return plan, nil
}

// Impact 返回某分区若被重写（版本加一），传递地将变为过期的已物化下游分区，
// 次序同 PlanBackfill。通过在同一致快照上模拟重写前后两次全量判定取差集得到，
// 因此与按定义逐分区重算严格一致；已过期者不算“将变为过期”，不进入结果。
func (e *Engine) Impact(assetName string, part int) ([]PartitionRef, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookup(assetName, part)
	if err != nil {
		return nil, err
	}
	before := e.staleSetLocked()
	p := a.stateOf(part)
	p.version++
	after := e.staleSetLocked()
	p.version--
	impact := make([]PartitionRef, 0)
	for ref := range after {
		if _, ok := before[ref]; !ok {
			impact = append(impact, ref)
		}
	}
	e.sortRefs(impact)
	return impact, nil
}

// staleSetLocked 返回当前全部已物化且过期的分区集合。调用方须持有锁。
func (e *Engine) staleSetLocked() map[PartitionRef]struct{} {
	memo := make(map[PartitionRef]bool)
	set := make(map[PartitionRef]struct{})
	for _, a := range e.assets {
		for part := range a.partitions {
			if e.staleLocked(a, part, memo) {
				set[PartitionRef{Asset: a.name, Partition: part}] = struct{}{}
			}
		}
	}
	return set
}
