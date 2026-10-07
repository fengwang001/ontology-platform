package ontology

import "sort"

// 本文件实现两类查询。查询只读取“该患者及其同病房相关记录”：
// 候选病例通过住宿索引按同病房重叠关系收集，与全院住宿记录总数无关。

// coLocated 返回与患者 p 的任一住宿区间有正时长同病房重叠的患者集合（含 p）。
func (e *Engine) coLocated(p string, now int64, into map[string]bool) {
	into[p] = true
	for _, s := range e.stays.ofPatient(p) {
		e.stats.StaysScanned++
		se := s.end(now)
		for _, t := range e.stays.ofWard(s.ward) {
			e.stats.StaysScanned++
			if t.patient == p {
				continue
			}
			if s.in < t.end(now) && t.in < se {
				into[t.patient] = true
			}
		}
	}
}

// candidateCases 收集查询患者状态时可能涉及的全部病例：
//
//   - 一跳：与 patient 同病房重叠过的患者所拥有的病例
//     （patient 可能是其密切接触者）；
//   - 二跳：与上述患者同病房重叠过的患者所拥有的病例
//     （patient 可能经某个密切接触者成为其次密接）。
//
// 次密接不再向外传递，因此两跳即完备。
func (e *Engine) candidateCases(patient string, now int64) []*caseRec {
	hop1 := make(map[string]bool)
	e.coLocated(patient, now, hop1)

	ids := make(map[string]bool)
	addCasesOf := func(p string) {
		for _, c := range e.cases.ofPatient(p) {
			if !c.revoked {
				ids[c.id] = true
			}
		}
	}
	for p := range hop1 {
		addCasesOf(p)
	}
	hop2 := make(map[string]bool)
	for p := range hop1 {
		if p == patient {
			continue // 与 patient 同病房者已在一跳收集
		}
		e.coLocated(p, now, hop2)
	}
	for p := range hop2 {
		addCasesOf(p)
	}

	out := make([]*caseRec, 0, len(ids))
	for id := range ids {
		out = append(out, e.cases.get(id))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (e *Engine) status(patient string, now int64) (StatusResult, error) {
	const op = "Status"
	if patient == "" {
		return StatusResult{}, newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return StatusResult{}, newErr(op, KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return StatusResult{}, err
	}
	e.acceptClock(now)

	sources := make([]Source, 0)
	for _, c := range e.candidateCases(patient, now) {
		e.ensureFresh(c, now)
		if info, ok := c.close[patient]; ok {
			rel := info.last + CloseIsolationMinutes
			sources = append(sources, Source{
				CaseID: c.id, Level: LevelClose, LastContact: info.last,
				ReleaseAt: rel, InPeriod: now < rel,
			})
		} else if info, ok := c.secondary[patient]; ok {
			rel := info.last + SecondaryObservationMinutes
			sources = append(sources, Source{
				CaseID: c.id, Level: LevelSecondary, LastContact: info.last,
				ReleaseAt: rel, InPeriod: now < rel,
			})
		}
	}

	res := StatusResult{Status: StatusNone, Sources: sources}
	if len(sources) == 0 {
		return res, nil
	}
	// 多病例叠加：仍在期内者取最严等级；解除时刻取各来源中最晚者。
	best := LevelSecondary
	inPeriod := false
	for _, s := range sources {
		if s.ReleaseAt > res.ReleaseAt {
			res.ReleaseAt = s.ReleaseAt
		}
		if s.InPeriod {
			inPeriod = true
			if s.Level > best {
				best = s.Level
			}
		}
	}
	switch {
	case inPeriod && best == LevelClose:
		res.Status = StatusClose
	case inPeriod:
		res.Status = StatusSecondary
	default:
		res.Status = StatusReleased
	}
	return res, nil
}

func (e *Engine) caseContacts(caseID string, now int64) (ContactsResult, error) {
	const op = "CaseContacts"
	if caseID == "" {
		return ContactsResult{}, newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return ContactsResult{}, newErr(op, KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return ContactsResult{}, err
	}
	c := e.cases.get(caseID)
	if c == nil {
		return ContactsResult{}, newErr(op, KindNotFound, "病例不存在")
	}
	e.acceptClock(now)
	res := ContactsResult{Close: []Contact{}, Secondary: []Contact{}}
	if c.revoked {
		// 已撤销的病例不再产生任何接触者。
		return res, nil
	}
	e.ensureFresh(c, now)
	for p, info := range c.close {
		res.Close = append(res.Close, Contact{
			PatientID: p, Minutes: info.minutes, LastContact: info.last,
		})
	}
	for p, info := range c.secondary {
		res.Secondary = append(res.Secondary, Contact{
			PatientID: p, Minutes: info.minutes, LastContact: info.last, Via: info.via,
		})
	}
	sort.Slice(res.Close, func(i, j int) bool { return res.Close[i].PatientID < res.Close[j].PatientID })
	sort.Slice(res.Secondary, func(i, j int) bool { return res.Secondary[i].PatientID < res.Secondary[j].PatientID })
	return res, nil
}
