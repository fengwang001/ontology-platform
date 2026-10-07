package batchimport

import "sort"

// seqEntry 给覆盖层值附加应用序号。
type seqEntry struct {
	inst Instance
	seq  int
}

// stagedMap 保存某类型下主键 -> 带应用序号的暂存条目。
type stagedMap map[string]seqEntry

// journalEntry 记录一次暂存写入前的覆盖层状态，用于按逆序精确撤销。
//
//	existed=false 表示写入前该主键不在覆盖层中（撤销时删除覆盖层条目，
//	重新露出底层全局存储；批次全程持锁，底层不会被其他批次改写）。
type journalEntry struct {
	typeName string
	id       string
	existed  bool
	prev     seqEntry
}

// staging 是批次级暂存覆盖层：承载已通过前置钩子校验的记录效果，
// 与全局存储叠加后构成钩子看到的只读视图，并记录撤销日志。
//
// 覆盖层按「应用顺序」线性增长，钩子在第 i 条记录触发时只能看到
// 应用序号 < cursor 的条目——严格遵循列表顺序：不是批次开始前的静态
// 快照，也不是整批完成后的最终状态。
type staging struct {
	overlay map[string]stagedMap
	journal []journalEntry
	// cursor 是当前钩子可见的覆盖层条目上界（已应用且已通过校验的条目数）。
	cursor int
	// steps 统计自上次 ResetSteps 起可见性解析实际触碰的覆盖层条目数，
	// 是「开销只与实际访问记录数相关、与批次总长度无关」的可验证证据。
	steps int
}

func newStaging() *staging {
	return &staging{overlay: map[string]stagedMap{}}
}

// apply 把一条通过前置钩子校验的记录效果写入覆盖层，推进可见上界，
// 并追加撤销日志。必须在持有 Registry 全局锁时调用。
func (s *staging) apply(rec Record) {
	bucket := s.overlay[rec.Type]
	if bucket == nil {
		bucket = stagedMap{}
		s.overlay[rec.Type] = bucket
	}
	entry := seqEntry{
		inst: Instance{Type: rec.Type, ID: rec.ID, Fields: cloneFields(rec.Fields)},
		seq:  s.cursor,
	}
	je := journalEntry{typeName: rec.Type, id: rec.ID}
	if prev, ok := bucket[rec.ID]; ok {
		je.existed, je.prev = true, prev
	}
	s.journal = append(s.journal, je)
	bucket[rec.ID] = entry
	s.cursor++
}

// get 解析单个主键在当前钩子可见范围内的实例状态：
// 先查覆盖层中「序号 < cursor」的条目，未命中再穿透到全局存储。
// 每次可见性解析只做一次哈希探测，开销为 O(1)，与批次总长度无关。
func (s *staging) get(r *Registry, typeName, id string) (Instance, bool) {
	if bucket := s.overlay[typeName]; bucket != nil {
		if e, ok := bucket[id]; ok && e.seq < s.cursor {
			s.steps++
			return e.inst.clone(), true
		}
	}
	inst, ok := r.data[typeName][id]
	if !ok {
		return Instance{}, false
	}
	return inst.clone(), true
}

// visibleIDs 枚举当前钩子可见的全部主键：全局存储中的全部主键，
// 叠加覆盖层中序号 < cursor 的条目（覆盖层同主键优先）。
func (s *staging) visibleIDs(r *Registry, typeName string) []string {
	set := map[string]struct{}{}
	for id := range r.data[typeName] {
		set[id] = struct{}{}
	}
	for id, e := range s.overlay[typeName] {
		if e.seq < s.cursor {
			set[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// rollback 按应用的逆序回放撤销日志，使覆盖层与（提交前不会被触碰的）
// 全局存储共同表达的状态与批次开始前完全一致；随后覆盖层应为空。
func (s *staging) rollback() {
	for i := len(s.journal) - 1; i >= 0; i-- {
		je := s.journal[i]
		bucket := s.overlay[je.typeName]
		if je.existed {
			bucket[je.id] = je.prev
		} else {
			delete(bucket, je.id)
		}
	}
	s.journal = s.journal[:0]
	s.cursor = 0
}

// commit 把覆盖层合并进全局存储并返回已应用记录的有序实例列表
// （序号即应用顺序）。必须在持有 Registry 全局锁时调用。
func (s *staging) commit(r *Registry) {
	for typeName, bucket := range s.overlay {
		dst := r.data[typeName]
		if dst == nil {
			dst = map[string]Instance{}
			r.data[typeName] = dst
		}
		for id, e := range bucket {
			dst[id] = e.inst
		}
	}
}

// ResetSteps / Steps 暴露可见性解析的实际步数以支持开销证明测试。
func (s *staging) ResetSteps() { s.steps = 0 }

func (s *staging) Steps() int { return s.steps }

func cloneFields(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
