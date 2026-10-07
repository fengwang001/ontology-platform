package ontology

// applyWriteLocked 在全局锁内将一条写入生效到对象存储，并维护所有已声明索引。
//
// 全局串行顺序：seq 单调递增且在同一把锁内同时完成“对象状态”与“可用索引”的
// 更新，因此对任意观察者，写入对对象生效的顺序与它对索引生效的顺序完全一致。
func (p *Platform) applyWriteLocked(t TypeID, obj ObjectID, a AttrName, v Value, op OpKind) Write {
	p.seq++
	w := Write{Seq: p.seq, Type: t, Object: obj, Attr: a, Value: v, Op: op}

	objs := p.objects[t]
	if objs == nil {
		objs = map[ObjectID]map[AttrName]AttrCell{}
		p.objects[t] = objs
	}
	cells := objs[obj]
	if cells == nil {
		cells = map[AttrName]AttrCell{}
		objs[obj] = cells
	}
	cells[a] = AttrCell{Value: v, Deleted: op == OpDelete, Version: w.Seq}

	p.writeLog = append(p.writeLog, w)
	p.maintainIndexLocked(w, cells)
	return w
}

// maintainIndexLocked 让“已可用”的索引随对象写入同步更新。
// 重建中（pending 非空）的索引不对外可用，其 delta 在 Complete 时统一按序重放。
func (p *Platform) maintainIndexLocked(w Write, cells map[AttrName]AttrCell) {
	st := p.indexes[indexKey{t: w.Type, a: w.Attr}]
	if st == nil {
		return
	}
	if st.pending != nil {
		// 重建期间的写入归入增量范围；基线快照已固定，不可回写 built。
		st.pending.deltas = append(st.pending.deltas, w)
		return
	}
	if st.status != StatusAvailable {
		return
	}
	// 对可用索引做在线更新（与对象状态同一临界区）。
	applyWriteToIndex(st.entries, w)
}

func applyWriteToIndex(entries map[Value]map[ObjectID]entryMeta, w Write) {
	for val, bucket := range entries {
		if _, ok := bucket[w.Object]; ok {
			delete(bucket, w.Object)
			if len(bucket) == 0 {
				delete(entries, val)
			}
		}
	}
	if w.Op == OpPut {
		bucket := entries[w.Value]
		if bucket == nil {
			bucket = map[ObjectID]entryMeta{}
			entries[w.Value] = bucket
		}
		bucket[w.Object] = entryMeta{sourceSeq: w.Seq}
	}
}
