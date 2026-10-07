package gc

import "sort"

// minorGCLocked 执行一次年轻区回收。起点为「根直接引用的年轻对象」与
// 「记忆集中登记的年老对象所引用的年轻对象」，不扫描年老区，因此开销只与
// 年轻区对象数、根数与记忆集大小相关（由 lastMinorWork 可验证）。
func (rt *Runtime) minorGCLocked() {
	rt.minorGCs++
	work := 0

	marked := make(map[ObjID]struct{})
	var mark func(id ObjID)
	mark = func(id ObjID) {
		o, ok := rt.objs[id]
		if !ok || !o.alive || o.gen != genYoung {
			return
		}
		if _, dup := marked[id]; dup {
			return
		}
		marked[id] = struct{}{}
		work++
		for _, f := range o.fields {
			mark(f)
		}
	}

	// 根：年轻根直接标记；年老根的年轻引用必由记忆集覆盖，这里防御性直扫其字段。
	for id := range rt.roots {
		work++
		o, ok := rt.objs[id]
		if !ok || !o.alive {
			continue
		}
		if o.gen == genYoung {
			mark(id)
		} else {
			for _, f := range o.fields {
				mark(f)
			}
		}
	}

	// 记忆集：只信任登记项本身，引用已死年轻对象的登记项不会使其复活。
	for id := range rt.remset {
		work++
		o, ok := rt.objs[id]
		if !ok || !o.alive || o.gen != genOld {
			continue
		}
		for _, f := range o.fields {
			mark(f)
		}
	}

	// 按 id 顺序处理年轻对象，保证晋升在年老区部分剩余时结果确定。
	youngs := make([]*object, 0, rt.youngN)
	for _, o := range rt.objs {
		if o.alive && o.gen == genYoung {
			youngs = append(youngs, o)
		}
	}
	sort.Slice(youngs, func(i, j int) bool { return youngs[i].id < youngs[j].id })

	var promoted []*object
	for _, o := range youngs {
		if _, live := marked[o.id]; !live {
			o.alive = false
			rt.youngN--
			continue
		}
		o.age++
		if o.age >= rt.threshold {
			if rt.oldN < rt.oldCap {
				o.gen = genOld
				rt.youngN--
				rt.oldN++
				rt.promotions++
				promoted = append(promoted, o)
			} else {
				// 晋升失败：留在年轻区并把回收次数钉在阈值，下次回收重试。
				o.age = rt.threshold
			}
		}
	}

	// 重算记忆集而非沿用：任何「年老->年轻」边只能由写屏障登记或晋升产生，
	// 因此新记忆集 = 旧登记项中仍引用活年轻对象者 + 晋升者中引用活年轻对象者。
	newRem := make(rememberedSet, len(rt.remset))
	for id := range rt.remset {
		if o, ok := rt.objs[id]; ok && o.alive && o.gen == genOld && rt.referencesYoung(o) {
			newRem.add(id)
		}
	}
	for _, o := range promoted {
		if rt.referencesYoung(o) {
			newRem.add(o.id)
		}
	}
	rt.remset = newRem
	rt.lastMinorWork = work
}

// referencesYoung 报告 o 是否引用了至少一个存活年轻对象。
func (rt *Runtime) referencesYoung(o *object) bool {
	for _, f := range o.fields {
		if t, ok := rt.objs[f]; ok && t.alive && t.gen == genYoung {
			return true
		}
	}
	return false
}
