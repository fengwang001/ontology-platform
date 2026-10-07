package gc

// rememberedSet 登记「可能指向年轻对象的年老对象」，按对象而非字段去重。
type rememberedSet map[ObjID]struct{}

func (s rememberedSet) add(id ObjID) {
	s[id] = struct{}{}
}

func (s rememberedSet) has(id ObjID) bool {
	_, ok := s[id]
	return ok
}

// writeBarrierLocked 与引用的实际写入不可分：调用方必须持有 rt.mu，
// 且紧邻字段赋值之后调用。仅当年老对象写入年轻对象时登记，单次开销 O(1)。
func (rt *Runtime) writeBarrierLocked(src, dst *object) {
	if src.gen == genOld && dst.gen == genYoung {
		rt.remset.add(src.id)
		rt.barrierRegs++
	}
}
