package ontology

import (
	"fmt"
	"sort"
)

// 复核报告中的不一致细分类别。
const (
	MismatchValue   = "entry_value_mismatch" // 条目值与对象当前属性值不符
	MismatchMissing = "entry_object_missing" // 条目指向的对象已不存在或属性已失值
	MismatchDup     = "entry_duplicate"      // 同一对象出现相互矛盾的多个条目
	MismatchAbsent  = "entry_missing"        // 对象当前有值但审计中缺条目
)

// Mismatch 是一条具体的、可定位到对象与索引键的条目级不一致。
type Mismatch struct {
	Kind      string        `json:"kind"`
	IndexName string        `json:"indexName"`
	IndexKey  string        `json:"indexKey"`
	ObjectID  string        `json:"objectId"`
	Property  string        `json:"property"`
	Expected  PropertyValue `json:"expected"`
	Actual    PropertyValue `json:"actual"`
	Detail    string        `json:"detail"`
}

func (mm Mismatch) String() string {
	return fmt.Sprintf("kind=%s index=%s key=%q object=%s expected=%q actual=%q detail=%s",
		mm.Kind, mm.IndexName, mm.IndexKey, mm.ObjectID, mm.Expected, mm.Actual, mm.Detail)
}

// VerifyReport 是复核结论。复核只读：不修改对象、不修改既有索引内容。
type VerifyReport struct {
	IndexName string `json:"indexName"`

	Consistent     bool       `json:"consistent"`
	DigestIntact   bool       `json:"digestIntact"`
	EntriesChecked int        `json:"entriesChecked"`
	Mismatches     []Mismatch `json:"mismatches"`

	// O(1) 取证指标：单条目复核读取的历史记录数是常数，不随历史总长度增长。
	HistoryRecordsRead int64 `json:"historyRecordsRead"`
	MaxHistoryPerEntry int64 `json:"maxHistoryPerEntry"`
	TotalObjectHistory int64 `json:"totalObjectHistory"`

	Basis string `json:"basis"`
}

// objectView 是某个对象在复核采集点上的不可变视图。
type objectView struct {
	current map[string]PropertyValue // 当前属性副本
	// sourceValid 按条目下标记录 SourceLSN 是否确为封存时点的生效版本。
	sourceValid []bool
	historyRead int64 // 该对象复核读取的历史记录数（<=2，常数）
	historyLen  int   // 该对象历史写入总次数（取证对照）
}

// WorldSnapshot 是在同一个 Store 串行点上原子采集的“审计 + 对象世界”。
// 复核判定只依赖该不可变快照，因此并发复核要么看到同一个串行点的世界，
// 要么看到之后某个同样自洽的串行点；对存活审计而言二者结论一致。
type WorldSnapshot struct {
	audit   *AuditRecord
	objects map[string]*objectView // 审计涉及对象
	liveIDs []string               // 该类型当前存在对象（反向检查用）
	live    map[string]map[string]PropertyValue
}

// Verifier 仅凭审计记录与对象当前状态做独立判定，不读取重建执行日志。
type Verifier struct {
	store *Store
	log   *DecisionLog
}

func NewVerifier(store *Store, log *DecisionLog) *Verifier {
	return &Verifier{store: store, log: log}
}

// CaptureWorldLocked 与状态机迁移在同一把 Store 锁内原子采集复核输入。
//
// 它复制当前属性、复制相关历史上的来源定位结果，使锁外判定不再触碰
// 可变状态。对每条审计条目至多做 2 次历史记录读取（来源 + 邻接边界），
// 计数随快照带出，用于规模无关性取证。
func CaptureWorldLocked(s *Store, rec *AuditRecord) *WorldSnapshot {
	w := &WorldSnapshot{
		audit:   rec,
		objects: make(map[string]*objectView),
		live:    make(map[string]map[string]PropertyValue),
	}
	for _, e := range rec.Entries {
		view := w.objects[e.ObjectID]
		if view == nil {
			view = &objectView{current: make(map[string]PropertyValue)}
			if obj := s.objects[e.ObjectID]; obj != nil && obj.exists {
				for k, vv := range obj.current {
					view.current[k] = vv
				}
			}
			view.historyLen = s.historyLengthLocked(e.ObjectID, rec.Property)
			w.objects[e.ObjectID] = view
		}
		before := s.historyInspections
		ok := evalEntrySource(s, rec, e)
		view.sourceValid = append(view.sourceValid, ok)
		view.historyRead += s.historyInspections - before
	}
	for _, oid := range s.objectIDsOfTypeLocked(rec.ObjectType) {
		cp := make(map[string]PropertyValue)
		if obj := s.objects[oid]; obj != nil {
			for k, vv := range obj.current {
				cp[k] = vv
			}
		}
		w.live[oid] = cp
		w.liveIDs = append(w.liveIDs, oid)
	}
	return w
}

// evalEntrySource 核验审计声明的来源 LSN 确为封存时点的生效版本。
// 每次调用至多读取 2 条历史记录（来源条目 + 邻接边界）。
func evalEntrySource(s *Store, rec *AuditRecord, e EntryProof) bool {
	_, _, ok := s.valueAtLocked(e.ObjectID, rec.Property, rec.CompletionLSN, e.SourceLSN)
	return ok
}

// checkWorld 对不可变世界快照做纯函数判定：同输入必然同输出。
func checkWorld(w *WorldSnapshot) *VerifyReport {
	rec := w.audit
	rep := &VerifyReport{
		IndexName:      rec.IndexName,
		DigestIntact:   rec.VerifyDigest(),
		EntriesChecked: len(rec.Entries),
		Basis: "sealed audit vs object state captured at one global serialization point; " +
			"rebuild execution logs are not read",
	}

	byObject := make(map[string][]int)
	for i, e := range rec.Entries {
		byObject[e.ObjectID] = append(byObject[e.ObjectID], i)
	}
	oids := make([]string, 0, len(byObject))
	for oid := range byObject {
		oids = append(oids, oid)
	}
	sort.Strings(oids)

	for _, oid := range oids {
		idxs := byObject[oid]
		view := w.objects[oid]
		curVal, hasCurrent := view.current[rec.Property]

		if len(idxs) > 1 {
			for _, j := range idxs[1:] {
				e := rec.Entries[j]
				rep.Mismatches = append(rep.Mismatches, Mismatch{
					Kind: MismatchDup, IndexName: rec.IndexName, IndexKey: e.IndexKey,
					ObjectID: oid, Property: rec.Property, Expected: curVal, Actual: e.Value,
					Detail: fmt.Sprintf("object appears %d times in audit", len(idxs)),
				})
			}
		}

		i := idxs[0]
		e := rec.Entries[i]
		switch {
		case !hasCurrent:
			rep.Mismatches = append(rep.Mismatches, Mismatch{
				Kind: MismatchMissing, IndexName: rec.IndexName, IndexKey: e.IndexKey,
				ObjectID: oid, Property: rec.Property, Expected: "(no current value)",
				Actual: e.Value, Detail: "entry points to object/property with no current value",
			})
		case e.Value != curVal || e.IndexKey != curVal:
			rep.Mismatches = append(rep.Mismatches, Mismatch{
				Kind: MismatchValue, IndexName: rec.IndexName, IndexKey: e.IndexKey,
				ObjectID: oid, Property: rec.Property, Expected: curVal, Actual: e.Value,
				Detail: "index entry value differs from object's current property value",
			})
		case i < len(view.sourceValid) && !view.sourceValid[i]:
			rep.Mismatches = append(rep.Mismatches, Mismatch{
				Kind: MismatchValue, IndexName: rec.IndexName, IndexKey: e.IndexKey,
				ObjectID: oid, Property: rec.Property, Expected: curVal, Actual: e.Value,
				Detail: fmt.Sprintf("claimed source LSN %d not effective at completion LSN %d",
					e.SourceLSN, rec.CompletionLSN),
			})
		}

		if view.historyRead > rep.MaxHistoryPerEntry {
			rep.MaxHistoryPerEntry = view.historyRead
		}
		rep.HistoryRecordsRead += view.historyRead
		rep.TotalObjectHistory += int64(view.historyLen)
	}

	for _, oid := range w.liveIDs {
		if _, indexed := byObject[oid]; indexed {
			continue
		}
		if curVal, has := w.live[oid][rec.Property]; has {
			rep.Mismatches = append(rep.Mismatches, Mismatch{
				Kind: MismatchAbsent, IndexName: rec.IndexName, IndexKey: curVal,
				ObjectID: oid, Property: rec.Property, Expected: curVal, Actual: "(missing)",
				Detail: "object currently has value but audit has no entry for it",
			})
		}
	}

	rep.Consistent = rep.DigestIntact && len(rep.Mismatches) == 0
	return rep
}

// VerifyManager 在单个串行点原子采集管理器存活审计与对象世界并复核。
//
// 采集与写入在同一把 Store 锁上排队，因此存活审计（随写入同点推进）
// 与采集到的对象状态天然自洽：并发复核结论不会因 TOCTOU 而相互矛盾。
func (v *Verifier) VerifyManager(m *IndexManager, objectType, indexName string,
	flagOnMismatch bool) (*VerifyReport, error) {
	s := v.store
	s.Lock()
	st, _, ok := m.stateLocked(objectType, indexName)
	if !ok {
		s.Unlock()
		err := fmt.Errorf("%w: %s/%s", ErrIndexNotDeclared, objectType, indexName)
		v.log.Write(DecisionRecord{
			Kind:     KindVerify,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName},
			Output:   map[string]interface{}{"error": err.Error()},
			Basis:    "verification requires a declared index",
			Decision: "rejected_not_declared",
		})
		return nil, err
	}
	if st.phase != PhaseActive || st.audit == nil {
		phase := st.phase
		s.Unlock()
		err := fmt.Errorf("%w: phase=%s for %s/%s",
			ErrIndexUnavailable, phase, objectType, indexName)
		v.log.Write(DecisionRecord{
			Kind:     KindVerify,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName},
			Output:   map[string]interface{}{"error": err.Error(), "phase": string(phase)},
			Basis:    "verification requires a complete sealed audit record",
			Decision: "rejected_unavailable",
		})
		return nil, err
	}

	s.historyInspections = 0
	world := CaptureWorldLocked(s, st.audit)
	auditCopy := *st.audit
	entries := make([]EntryProof, len(st.audit.Entries))
	copy(entries, st.audit.Entries)
	auditCopy.Entries = entries
	world.audit = &auditCopy
	s.Unlock()

	rep := checkWorld(world)
	if flagOnMismatch && !rep.Consistent {
		m.FlagInconsistent(objectType, indexName,
			fmt.Sprintf("independent verify found %d entry-level mismatch(es)",
				len(rep.Mismatches)))
	}

	mismatchSummary := make([]string, 0, len(rep.Mismatches))
	for _, mm := range rep.Mismatches {
		mismatchSummary = append(mismatchSummary, mm.String())
	}
	v.log.Write(DecisionRecord{
		Kind: KindVerify,
		Input: map[string]interface{}{
			"objectType": objectType, "indexName": indexName,
			"entries": len(auditCopy.Entries), "digest": auditCopy.Digest,
		},
		Output: map[string]interface{}{
			"consistent":         rep.Consistent,
			"historyRecordsRead": rep.HistoryRecordsRead,
			"maxHistoryPerEntry": rep.MaxHistoryPerEntry,
			"mismatches":         mismatchSummary,
		},
		Basis:    rep.Basis,
		Decision: map[bool]string{true: "consistent", false: "inconsistent"}[rep.Consistent],
	})
	return rep, nil
}

// VerifyAudit 复核任意外部来源的审计记录与 Store 当前对象状态。
//
// 采集仍在单个串行点完成，随后锁外纯函数判定。注意：外部审计若早于
// 某些并发写入封存，它与“当前”状态可以合法不同；管理器路径
// （VerifyManager）因审计与写入同点推进而不存在这种情形。
func (v *Verifier) VerifyAudit(rec *AuditRecord, s *Store) *VerifyReport {
	if rec == nil {
		return &VerifyReport{Consistent: false, Basis: "nil audit record"}
	}
	s.Lock()
	s.historyInspections = 0
	world := CaptureWorldLocked(s, rec)
	s.Unlock()
	return checkWorld(world)
}

// VerifyEntry 只复核审计中某个对象对应的单条条目，不做全量枚举。
//
// 规模无关性：无论对象历史多长，来源核验最多读 2 条历史记录；
// 采集点的读取计数可由调用方经 Store.HistoryInspections 独立验证。
func (v *Verifier) VerifyEntry(rec *AuditRecord, s *Store, objectID string) (*Mismatch, error) {
	if rec == nil {
		return nil, ErrIndexUnavailable
	}
	s.Lock()
	var found *EntryProof
	for i := range rec.Entries {
		if rec.Entries[i].ObjectID == objectID {
			e := rec.Entries[i]
			found = &e
			break
		}
	}
	current := map[string]PropertyValue{}
	if obj := s.objects[objectID]; obj != nil && obj.exists {
		for k, vv := range obj.current {
			current[k] = vv
		}
	}
	sourceOK := true
	if found != nil {
		sourceOK = evalEntrySource(s, rec, *found)
	}
	s.Unlock()

	curVal, hasCurrent := current[rec.Property]
	if found == nil {
		if hasCurrent {
			return &Mismatch{
				Kind: MismatchAbsent, IndexName: rec.IndexName, IndexKey: curVal,
				ObjectID: objectID, Property: rec.Property, Expected: curVal,
				Actual: "(missing)", Detail: "no audit entry for object",
			}, nil
		}
		return nil, nil
	}
	switch {
	case !hasCurrent:
		return &Mismatch{
			Kind: MismatchMissing, IndexName: rec.IndexName, IndexKey: found.IndexKey,
			ObjectID: objectID, Property: rec.Property, Expected: "(no current value)",
			Actual: found.Value, Detail: "object/property has no current value",
		}, nil
	case found.Value != curVal || found.IndexKey != curVal:
		return &Mismatch{
			Kind: MismatchValue, IndexName: rec.IndexName, IndexKey: found.IndexKey,
			ObjectID: objectID, Property: rec.Property, Expected: curVal,
			Actual: found.Value,
			Detail: "index entry value differs from object's current property value",
		}, nil
	case !sourceOK:
		return &Mismatch{
			Kind: MismatchValue, IndexName: rec.IndexName, IndexKey: found.IndexKey,
			ObjectID: objectID, Property: rec.Property, Expected: curVal,
			Actual: found.Value,
			Detail: fmt.Sprintf("claimed source LSN %d not effective at completion LSN %d",
				found.SourceLSN, rec.CompletionLSN),
		}, nil
	}
	return nil, nil
}
