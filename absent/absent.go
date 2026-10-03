// Package absent 维护单个分区的存活表 T 与每键缺席连续计数 a，
// 并依据连续 K 轮完整扫描的缺席结果产生候选删除集合。
// 本包不加锁：所有方法都假定调用方（scan）持有该分区的分片锁。
package absent

// WriteOutcome 描述一次 Report 逐项写入的结果。
type WriteOutcome int

const (
	Same   WriteOutcome = iota // 键已存在且值相同
	Insert                     // 键不存在
	Update                     // 键存在但值不同
)

// Row 是源表的一行。
type Row struct {
	K int64
	V int64
}

// Partition 是一个分区的存活表与缺席计数。
type Partition struct {
	kThreshold int
	values     map[int64]int64 // 存活键 -> 值（T）
	absent     map[int64]int   // 存活键 -> 连续缺席完整扫描轮数 a
}

// New 创建分区状态；k 为判定删除所需的连续缺席轮数（1..10）。
func New(k int) *Partition {
	return &Partition{
		kThreshold: k,
		values:     make(map[int64]int64),
		absent:     make(map[int64]int),
	}
}

// Observe 记录一次"见到"：插入或更新 v，返回写入类型，并把缺席计数置 0。
// seen 非空时，同时把该键标记为本会话已见。
func (p *Partition) Observe(k, v int64, seen map[int64]struct{}) WriteOutcome {
	outcome := Insert
	if old, ok := p.values[k]; ok {
		if old == v {
			outcome = Same
		} else {
			outcome = Update
		}
	}
	p.values[k] = v
	// 见到即存在的证据：无论会话最终是否完整，a 立即归 0。
	p.absent[k] = 0
	if seen != nil {
		seen[k] = struct{}{}
	}
	return outcome
}

// Accumulate 在一轮完整扫描结束时执行：所有存活但未在 seen 中的键 a 加 1。
func (p *Partition) Accumulate(seen map[int64]struct{}) {
	for k := range p.values {
		if _, ok := seen[k]; ok {
			continue
		}
		p.absent[k]++
	}
}

// Snapshot 返回 End 开始时刻的 n0（存活键数）与候选集合 C（a>=K 的键）。
func (p *Partition) Snapshot() (n0 int, candidates []int64) {
	n0 = len(p.values)
	for k, a := range p.absent {
		if a >= p.kThreshold {
			candidates = append(candidates, k)
		}
	}
	return n0, candidates
}

// DeleteCandidates 移除 candidates 中的全部存活键并丢弃其 a，返回实际删除数。
func (p *Partition) DeleteCandidates(candidates []int64) int {
	deleted := 0
	for _, k := range candidates {
		if _, ok := p.values[k]; !ok {
			continue
		}
		delete(p.values, k)
		delete(p.absent, k)
		deleted++
	}
	return deleted
}

// DeleteAbsent 删除当前全部 a>=K 的键（人工放行路径），不再判比例，返回删除数。
func (p *Partition) DeleteAbsent() int {
	_, candidates := p.Snapshot()
	return p.DeleteCandidates(candidates)
}

// LiveCount 返回当前存活键数（测试/对拍用）。
func (p *Partition) LiveCount() int {
	return len(p.values)
}

// Get 返回存活值与是否存在（测试/对拍用）。
func (p *Partition) Get(k int64) (int64, bool) {
	v, ok := p.values[k]
	return v, ok
}

// AbsentCount 返回某键当前的缺席计数；不存在返回 0（测试/对拍用）。
func (p *Partition) AbsentCount(k int64) int {
	return p.absent[k]
}

// Dump 返回存活键->值与存活键->a 的副本快照（检视/测试用）。
func (p *Partition) Dump() (map[int64]int64, map[int64]int) {
	values := make(map[int64]int64, len(p.values))
	absents := make(map[int64]int, len(p.absent))
	for k, v := range p.values {
		values[k] = v
	}
	for k, a := range p.absent {
		absents[k] = a
	}
	return values, absents
}
