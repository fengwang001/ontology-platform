package gc

// majorGCLocked 执行一次年老区回收：扫描整个托管空间，从根集合出发判定存活，
// 清除死亡的年老对象并移除记忆集中相应登记项。不改变任何对象的回收次数，
// 不回收年轻对象（留给年轻区回收），只能由显式触发。
func (rt *Runtime) majorGCLocked() {
	rt.majorGCs++

	marked := make(map[ObjID]struct{})
	var mark func(id ObjID)
	mark = func(id ObjID) {
		o, ok := rt.objs[id]
		if !ok || !o.alive {
			return
		}
		if _, dup := marked[id]; dup {
			return
		}
		marked[id] = struct{}{}
		for _, f := range o.fields {
			mark(f)
		}
	}
	for id := range rt.roots {
		mark(id)
	}

	for _, o := range rt.objs {
		if !o.alive || o.gen != genOld {
			continue
		}
		if _, live := marked[o.id]; !live {
			o.alive = false
			rt.oldN--
			delete(rt.remset, o.id)
		}
	}
}
