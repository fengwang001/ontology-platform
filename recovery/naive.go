package recovery

// naiveReplay 是独立实现的朴素重放参照模型。
//
// 与 engine 的刻意区别：不构造任何对象级锚点索引之外的结构，
// 不共享 engine 的辅助函数（除状态拼接约定外），先对每条动作
// 做线性可行性扫描，再线性应用，直白对照需求条文。
func naiveReplay(snap Snapshot, actions []Action) map[ObjectID]ObjectReport {
	// snapshotOK / snapshotBad：存在性、完好、损坏三分。
	snapshotOK := map[ObjectID]bool{}
	snapshotBad := map[ObjectID]bool{}
	value := map[ObjectID]State{}

	for _, rec := range snap.Records {
		if SnapshotChecksum(rec.ObjectID, rec.State) == rec.Checksum && rec.Checksum != "" {
			snapshotOK[rec.ObjectID] = true
			value[rec.ObjectID] = rec.State
		} else {
			snapshotBad[rec.ObjectID] = true
		}
	}

	// firstActionIndex[obj] = 最早让对象进入可读状态的动作下标；
	// -1 表示快照已可读。
	firstActionIndex := map[ObjectID]int{}
	for obj := range snapshotOK {
		firstActionIndex[obj] = -1
	}

	naiveApply := func(base, change State) State {
		if change == "" {
			return base
		}
		return base + "|" + change
	}

	for i := 0; i < len(actions); i++ {
		act := actions[i]

		// 原子性预检：任何对象起点未知且无自带起点 -> 整条动作无效。
		feasible := true
		for obj, ef := range act.Effects {
			_, currentlyKnown := value[obj]
			if !currentlyKnown && !ef.HasStart {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}

		// 整体应用。
		for obj, ef := range act.Effects {
			if prior, known := value[obj]; known {
				base := prior
				if ef.HasStart {
					base = ef.Start
				}
				value[obj] = naiveApply(base, ef.Change)
			} else {
				firstActionIndex[obj] = i
				value[obj] = naiveApply(ef.Start, ef.Change)
			}
		}
	}

	covered := map[ObjectID]struct{}{}
	for obj := range snapshotOK {
		covered[obj] = struct{}{}
	}
	for obj := range snapshotBad {
		covered[obj] = struct{}{}
	}
	for _, act := range actions {
		for obj := range act.Effects {
			covered[obj] = struct{}{}
		}
	}

	out := make(map[ObjectID]ObjectReport, len(covered))
	for obj := range covered {
		rep := ObjectReport{ObjectID: obj, AnchorIndex: -1}
		switch {
		case snapshotBad[obj]:
			rep.Snapshot = StatusCorrupt
		case snapshotOK[obj]:
			rep.Snapshot = StatusValid
		default:
			rep.Snapshot = StatusAbsent
		}
		if final, known := value[obj]; known {
			rep.FinalState = final
			if rep.Snapshot == StatusValid && firstActionIndex[obj] == -1 {
				rep.Source = SourceSnapshot
			} else {
				rep.Source = SourceReplay
				rep.AnchorIndex = firstActionIndex[obj]
			}
		} else {
			rep.Source = SourceUnreadable
		}
		out[obj] = rep
	}
	return out
}
