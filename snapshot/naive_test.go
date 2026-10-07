package snapshot

// 本文件是朴素参照模型：一个刻意简单、逐步独立判定的导出实现，
// 用于在随机写入序列上与真实实现对照。它不共享真实实现的任何
// 内部逻辑，每条规则都在这里被直接、可读地重新表达一遍。

// naiveBoundary 独立重算自动边界：从尖端向下找到第一个不切分
// 任何事务的位置（规则 R3 的朴素表达）。
func naiveBoundary(writes []Write) LSN {
	tip := LSN(len(writes))
	for n := tip; ; n-- {
		if n == 0 || naiveTxnSafe(writes, n) {
			return n
		}
	}
}

func naiveTxnSafe(writes []Write, n LSN) bool {
	before := map[TxnID]bool{}
	for _, w := range writes {
		if w.LSN <= n {
			before[w.Txn] = true
		}
	}
	for _, w := range writes {
		if w.LSN > n && before[w.Txn] {
			return false
		}
	}
	return true
}

// naiveClassify 独立重述归属判定（规则 R1）。
func naiveClassify(lsn, boundary LSN) Side {
	if lsn <= boundary {
		return SideSnapshot
	}
	return SideIncrement
}

// naiveExport 对给定写入序列与边界，朴素地重放整个导出过程，
// 返回快照、增量序列与首个错误（若有）。检查顺序与真实实现
// 一致：快照引用完整性，再按序逐条增量（先原子性后引用完整性）。
func naiveExport(writes []Write, boundary LSN) (State, []Increment, *ExportError) {
	state := NewState()
	for _, w := range writes {
		if naiveClassify(w.LSN, boundary) == SideSnapshot {
			state.Apply(w)
		}
	}
	// 快照引用完整性（R5 的朴素表达）。
	for id, l := range state.Links {
		_, srcOK := state.Objects[l.Src]
		_, dstOK := state.Objects[l.Dst]
		if !srcOK || !dstOK {
			return state, nil, &ExportError{
				Kind:   ErrReferentialConflict,
				Rule:   RuleLinkEndpointsFirst,
				Detail: "naive: dangling link in snapshot " + id,
			}
		}
	}
	visible := map[string]bool{}
	for id := range state.Objects {
		visible[id] = true
	}
	closed := map[TxnID]bool{}
	var incrs []Increment
	for i := 0; i < len(writes); {
		w := writes[i]
		if naiveClassify(w.LSN, boundary) == SideSnapshot {
			i++
			continue
		}
		// 聚合连续同事务写入。
		incr := Increment{Txn: w.Txn, FromLSN: w.LSN}
		for i < len(writes) && writes[i].Txn == w.Txn {
			incr.Writes = append(incr.Writes, writes[i])
			incr.ToLSN = writes[i].LSN
			i++
		}
		// 原子性（R4 的朴素表达）。
		if closed[incr.Txn] {
			return state, incrs, &ExportError{
				Kind:   ErrAtomicityConflict,
				Rule:   RuleTxnContiguous,
				Detail: "naive: non-contiguous txn " + string(incr.Txn),
				LSN:    incr.FromLSN,
			}
		}
		// 引用完整性（R5 的朴素表达）。
		created := map[string]bool{}
		for _, ww := range incr.Writes {
			if ww.Kind == KindObjectUpsert {
				created[ww.Object.ID] = true
			}
		}
		for _, ww := range incr.Writes {
			if ww.Kind != KindLinkUpsert {
				continue
			}
			for _, ep := range [2]string{ww.Link.Src, ww.Link.Dst} {
				if !visible[ep] && !created[ep] {
					return state, incrs, &ExportError{
						Kind:   ErrReferentialConflict,
						Rule:   RuleLinkEndpointsFirst,
						Detail: "naive: dangling link " + ww.Link.ID,
						LSN:    ww.LSN,
					}
				}
			}
		}
		for id := range created {
			visible[id] = true
		}
		closed[incr.Txn] = true
		incrs = append(incrs, incr)
	}
	return state, incrs, nil
}

// --- 构造写入的辅助函数 ---

func objSpec(txn TxnID, id string) WriteSpec {
	return WriteSpec{Txn: txn, Kind: KindObjectUpsert, Object: &Object{ID: id, Type: "T"}}
}

func linkSpec(txn TxnID, id, src, dst string) WriteSpec {
	return WriteSpec{Txn: txn, Kind: KindLinkUpsert, Link: &Link{ID: id, Type: "L", Src: src, Dst: dst}}
}

func actSpec(txn TxnID, id string) WriteSpec {
	return WriteSpec{Txn: txn, Kind: KindActionRecord, Action: &ActionRecord{ID: id, Action: "A"}}
}
