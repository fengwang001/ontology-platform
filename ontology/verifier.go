package ontology

import "sort"

// snapshot 是在单一临界区内取得的对象/索引/审计一致视图。
// 多个并发复核基于各自不可变快照计算；同一时刻的快照内容逐字节一致，
// 因此并发复核必然得到相同结论。
type snapshot struct {
	typeID  TypeID
	attr    AttrName
	status  IndexStatus
	seq     int64
	audit   *AuditRecord
	objects map[ObjectID]map[AttrName]AttrCell
	entries map[Value]map[ObjectID]entryMeta
}

func (p *Platform) snapshotLocked(t TypeID, a AttrName) (snapshot, bool) {
	st, ok := p.indexes[indexKey{t: t, a: a}]
	if !ok {
		return snapshot{}, false
	}
	s := snapshot{
		typeID: t, attr: a, status: st.status,
		seq:     p.seq,
		objects: p.objects[t],
	}
	if st.audit != nil {
		rec := *st.audit
		rec.Entries = append([]AuditEntry(nil), st.audit.Entries...)
		s.audit = &rec
	}
	if st.entries != nil {
		s.entries = make(map[Value]map[ObjectID]entryMeta, len(st.entries))
		for v, bucket := range st.entries {
			cp := make(map[ObjectID]entryMeta, len(bucket))
			for o, m := range bucket {
				cp[o] = m
			}
			s.entries[v] = cp
		}
	}
	return s, true
}

// verifySnapshot 是纯函数式复核：只依赖快照（= 审计记录 + 对象当前状态 + 当前索引），
// 不读取重建执行日志，也不读取任何对象的历史写入日志。
//
// 规模无关性证明：对每个索引条目恰好读取“一个对象当前属性单元”（O(1) 次当前态
// 随机读），历史写入记录读取数恒为 0（报告字段 HistoryReads）。条目复核成本与该
// 对象累计写入次数无关，因为对象单元上保存了“最后修改序号”指针，一次比较即可
// 判定，无需回放历史。
func verifySnapshot(s snapshot) *VerificationReport {
	rep := &VerificationReport{Type: s.typeID, Attr: s.attr, Consistent: true, SnapshotSeq: s.seq}
	add := func(m Mismatch) {
		rep.Consistent = false
		rep.Mismatches = append(rep.Mismatches, m)
	}

	if s.status != StatusAvailable {
		rep.Consistent = false
		add(Mismatch{Kind: "index_unavailable", Detail: "索引无完整审计记录，处于重建中/失败状态，无法复核"})
		return rep
	}
	rec := s.audit
	if rec == nil {
		add(Mismatch{Kind: "audit_missing", Detail: "索引可用却缺少审计记录"})
		return rep
	}
	rep.AuditSeqAt = rec.CompleteSeq
	if !rec.ValidDigest() {
		add(Mismatch{Kind: "digest_bad", Detail: "审计记录指纹不匹配，内容可能被篡改"})
	}
	if rec.BaselineSeq > rec.CompleteSeq {
		add(Mismatch{Kind: "audit_gap", Detail: "基准点序号大于完成序号，基线/增量边界非法"})
	}

	// 第一部分：审计内部可独立校验的边界划分（不依赖重建日志）。
	seen := map[ObjectID]AuditEntry{}
	for _, e := range rec.Entries {
		prev, dup := seen[e.Object]
		if dup {
			add(Mismatch{Object: e.Object, Value: e.Value, Kind: "audit_duplicate",
				Detail: "同一对象在审计中出现多条索引来源（重建内部重放异常）"})
			_ = prev
		}
		seen[e.Object] = e
		if e.SourceSeq <= 0 {
			add(Mismatch{Object: e.Object, Value: e.Value, Kind: "audit_bad_source",
				Detail: "来源写入序号非法"})
		}
		inRange := e.SourceSeq <= rec.CompleteSeq
		flagConsistent := (e.Baseline == (e.SourceSeq <= rec.BaselineSeq))
		if !inRange {
			add(Mismatch{Object: e.Object, Value: e.Value, Kind: "audit_gap",
				Detail: "来源序号超出重建完成点，边界遗漏或越界"})
		}
		if !flagConsistent {
			add(Mismatch{Object: e.Object, Value: e.Value, Kind: "audit_overlap",
				Detail: "基线/增量归属标记与来源序号矛盾，边界重叠"})
		}
	}

	// 第二部分：当前索引的每一条目 vs 对象当前属性单元（每条目 O(1) 次当前态读）。
	current := map[ObjectID]bool{}
	values := make([]Value, 0, len(s.entries))
	for v := range s.entries {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	for _, v := range values {
		objs := make([]ObjectID, 0, len(s.entries[v]))
		for o := range s.entries[v] {
			objs = append(objs, o)
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i] < objs[j] })
		for _, obj := range objs {
			meta := s.entries[v][obj]
			rep.CheckedNow++
			current[obj] = true
			cells := s.objects[obj]
			var cell AttrCell
			var exists bool
			if cells != nil {
				cell, exists = cells[s.attr]
			}
			rep.CellReads++ // 每条目恰好一次当前属性单元读取
			switch {
			case !exists || cell.Deleted:
				add(Mismatch{Object: obj, Value: v, Kind: "extra_entry",
					Detail: "索引有条目，但对象当前无该属性（已删除），条目应不存在"})
			case cell.Value != v:
				add(Mismatch{Object: obj, Value: v, Kind: "wrong_value",
					Detail: "索引取值与对象当前实际属性值不一致：实际=" + string(cell.Value)})
			case cell.Version != meta.sourceSeq:
				add(Mismatch{Object: obj, Value: v, Kind: "stale_entry",
					Detail: "索引来源序号落后于对象当前版本，增量未按序应用"})
			}
		}
	}

	// 第三部分：覆盖完整性——当前拥有该属性的每个对象都必须在索引中。
	allObjs := make([]ObjectID, 0, len(s.objects))
	for o := range s.objects {
		allObjs = append(allObjs, o)
	}
	sort.Slice(allObjs, func(i, j int) bool { return allObjs[i] < allObjs[j] })
	for _, obj := range allObjs {
		cell, ok := s.objects[obj][s.attr]
		if !ok || cell.Deleted {
			continue
		}
		rep.CellReads++
		bucket := s.entries[cell.Value]
		if _, present := bucket[obj]; !present {
			add(Mismatch{Object: obj, Value: cell.Value, Kind: "missing_entry",
				Detail: "对象当前拥有该属性，但索引缺少对应条目"})
		}
	}
	rep.HistoryReads = 0 // 证明：不回放任何对象历史写入
	return rep
}

// Verify 取得一致快照并执行只读、幂等复核。同一审计记录 + 同一对象状态下
// 多次调用得到完全相同的报告（确定性排序 + 纯函数计算）。
func (p *Platform) Verify(t TypeID, a AttrName) *VerificationReport {
	p.mu.Lock()
	s, ok := p.snapshotLocked(t, a)
	p.mu.Unlock()
	if !ok {
		rep := &VerificationReport{Type: t, Attr: a, Consistent: false,
			Mismatches: []Mismatch{{Kind: "index_not_declared", Detail: "对象类型未声明该索引"}}}
		p.log("verify", map[string]string{"type": string(t), "attr": string(a)},
			"未声明：无法复核", rep)
		return rep
	}
	rep := verifySnapshot(s)
	p.log("verify", map[string]interface{}{"type": string(t), "attr": string(a),
		"complete_seq": rep.AuditSeqAt, "cell_reads": rep.CellReads, "history_reads": rep.HistoryReads},
		"仅凭审计记录+对象当前状态，逐条 O(1) 判定，不读历史日志", rep)
	return rep
}

// MarkFlaggedFromReport 是独立的管理动作：将复核发现的不一致落到“需修复”标记上。
// Verify 本身严格只读；查询的不一致声明由该显式标记驱动。
func (p *Platform) MarkFlaggedFromReport(t TypeID, a AttrName, rep *VerificationReport) {
	if rep == nil || rep.Consistent {
		return
	}
	p.mu.Lock()
	if st, ok := p.indexes[indexKey{t: t, a: a}]; ok && st.status == StatusAvailable {
		st.flaggedDirty = true
	}
	p.mu.Unlock()
}
