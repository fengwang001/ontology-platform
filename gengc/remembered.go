package gengc

// rememberedSet 记录“可能指向年轻对象”的年老对象集合。
// 按对象登记而非按字段：同一对象多次写入只算一次。
type rememberedSet struct {
	members map[uint64]struct{}
	// barrierEntries 累计写屏障登记次数：一次新成员加入 +1。
	barrierEntries int
}

func newRememberedSet() *rememberedSet {
	return &rememberedSet{members: make(map[uint64]struct{})}
}

// add 登记一个年老对象。返回 true 表示新加入（计入屏障统计）。
func (r *rememberedSet) add(id uint64) bool {
	if _, ok := r.members[id]; ok {
		return false
	}
	r.members[id] = struct{}{}
	r.barrierEntries++
	return true
}

func (r *rememberedSet) remove(id uint64) { delete(r.members, id) }

func (r *rememberedSet) contains(id uint64) bool {
	_, ok := r.members[id]
	return ok
}

func (r *rememberedSet) size() int { return len(r.members) }

func (r *rememberedSet) snapshot() (map[uint64]struct{}, int) {
	m := make(map[uint64]struct{}, len(r.members))
	for id := range r.members {
		m[id] = struct{}{}
	}
	return m, r.barrierEntries
}

func (r *rememberedSet) restore(m map[uint64]struct{}, entries int) {
	cp := make(map[uint64]struct{}, len(m))
	for id := range m {
		cp[id] = struct{}{}
	}
	r.members = cp
	r.barrierEntries = entries
}

// writeBarrier 实现卡表式写屏障。登记条件（两者皆须满足）：
//  1. 被写入字段的承载对象 src 位于年老区；
//  2. 写入的目标 dst 位于年轻区。
//
// 写入年老目标、或写入年轻对象字段，均不登记。
// 单次开销 O(1)，与堆规模无关。
func (h *Heap) writeBarrier(src, dst uint64) {
	srcObj, dstObj := h.objects[src], h.objects[dst]
	if srcObj == nil || dstObj == nil {
		return
	}
	if srcObj.gen == GenOld && dstObj.gen == GenYoung {
		h.rs.add(src)
	}
}
