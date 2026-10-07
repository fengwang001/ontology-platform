package naive

import (
	"sort"

	"ontology"
)

// 朴素推导：对病例 c，扫描全部住宿记录，按定义直接计算密接与次密接。

type accInfo struct {
	minutes int64
	first   int64
	last    int64
}

func overlap(a1, b1, a2, b2 int64) (int64, int64, bool) {
	a := a1
	if a2 > a {
		a = a2
	}
	b := b1
	if b2 < b {
		b = b2
	}
	return a, b, a < b
}

// derive 返回 (密接 patient->acc, 次密接 patient->Contact)。
func (e *Engine) derive(c *caseRec, now int64) (map[string]*accInfo, map[string]ontology.Contact) {
	closeAcc := make(map[string]*accInfo)
	lo := c.infectiousStart()
	hi := now
	if c.isolated {
		hi = c.isolatedAt
	}
	if hi > lo {
		for _, s := range e.stays {
			if s.patient != c.patient {
				continue
			}
			a, b, ok := overlap(s.in, s.end(now), lo, hi)
			if !ok {
				continue
			}
			for _, t := range e.stays {
				if t.ward != s.ward || t.patient == c.patient {
					continue
				}
				oa, ob, ok := overlap(a, b, t.in, t.end(now))
				if !ok {
					continue
				}
				acc := closeAcc[t.patient]
				if acc == nil {
					acc = &accInfo{first: oa, last: ob}
					closeAcc[t.patient] = acc
				}
				acc.minutes += ob - oa
				if oa < acc.first {
					acc.first = oa
				}
				if ob > acc.last {
					acc.last = ob
				}
			}
		}
	}
	for p, acc := range closeAcc {
		if acc.minutes < ontology.ContactThresholdMinutes {
			delete(closeAcc, p)
		}
	}

	// 次密接：与某密接 q 在其暴露期 [首个重叠起点, 确诊登记时刻) 内
	// 同病房累计达阈值者（非密接、非病例本人）。
	type pair struct{ x, q string }
	secAcc := make(map[pair]*accInfo)
	for q, qa := range closeAcc {
		expLo, expHi := qa.first, c.registeredAt
		if expHi <= expLo {
			continue
		}
		for _, s := range e.stays {
			if s.patient != q {
				continue
			}
			a, b, ok := overlap(s.in, s.end(now), expLo, expHi)
			if !ok {
				continue
			}
			for _, t := range e.stays {
				if t.ward != s.ward || t.patient == q || t.patient == c.patient {
					continue
				}
				oa, ob, ok := overlap(a, b, t.in, t.end(now))
				if !ok {
					continue
				}
				k := pair{x: t.patient, q: q}
				acc := secAcc[k]
				if acc == nil {
					acc = &accInfo{first: oa, last: ob}
					secAcc[k] = acc
				}
				acc.minutes += ob - oa
				if ob > acc.last {
					acc.last = ob
				}
			}
		}
	}
	secondary := make(map[string]ontology.Contact)
	for k, acc := range secAcc {
		if acc.minutes < ontology.ContactThresholdMinutes {
			continue
		}
		if _, isClose := closeAcc[k.x]; isClose {
			continue
		}
		cur, ok := secondary[k.x]
		switch {
		case !ok:
			secondary[k.x] = ontology.Contact{PatientID: k.x, Minutes: acc.minutes, LastContact: acc.last, Via: k.q}
		case acc.minutes > cur.Minutes || (acc.minutes == cur.Minutes && k.q < cur.Via):
			last := acc.last
			if cur.LastContact > last {
				last = cur.LastContact
			}
			secondary[k.x] = ontology.Contact{PatientID: k.x, Minutes: acc.minutes, LastContact: last, Via: k.q}
		case acc.last > cur.LastContact:
			cur.LastContact = acc.last
			secondary[k.x] = cur
		}
	}
	return closeAcc, secondary
}

// Status 查询患者在 now 的隔离状态。
func (e *Engine) Status(patient string, now int64) (ontology.StatusResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Status"
	if patient == "" {
		return ontology.StatusResult{}, errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return ontology.StatusResult{}, errOf(op, ontology.KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return ontology.StatusResult{}, err
	}
	e.acceptClock(now)

	sources := make([]ontology.Source, 0)
	for _, c := range e.cases {
		if c.revoked {
			continue
		}
		closeAcc, secondary := e.derive(c, now)
		if acc, ok := closeAcc[patient]; ok {
			rel := acc.last + ontology.CloseIsolationMinutes
			sources = append(sources, ontology.Source{
				CaseID: c.id, Level: ontology.LevelClose, LastContact: acc.last,
				ReleaseAt: rel, InPeriod: now < rel,
			})
		} else if ct, ok := secondary[patient]; ok {
			rel := ct.LastContact + ontology.SecondaryObservationMinutes
			sources = append(sources, ontology.Source{
				CaseID: c.id, Level: ontology.LevelSecondary, LastContact: ct.LastContact,
				ReleaseAt: rel, InPeriod: now < rel,
			})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].CaseID < sources[j].CaseID })

	res := ontology.StatusResult{Status: ontology.StatusNone, Sources: sources}
	if len(sources) == 0 {
		return res, nil
	}
	best := ontology.LevelSecondary
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
	case inPeriod && best == ontology.LevelClose:
		res.Status = ontology.StatusClose
	case inPeriod:
		res.Status = ontology.StatusSecondary
	default:
		res.Status = ontology.StatusReleased
	}
	return res, nil
}

// CaseContacts 查询病例当前的密接与次密接清单。
func (e *Engine) CaseContacts(caseID string, now int64) (ontology.ContactsResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "CaseContacts"
	if caseID == "" {
		return ontology.ContactsResult{}, errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return ontology.ContactsResult{}, errOf(op, ontology.KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return ontology.ContactsResult{}, err
	}
	c := e.byID[caseID]
	if c == nil {
		return ontology.ContactsResult{}, errOf(op, ontology.KindNotFound, "病例不存在")
	}
	e.acceptClock(now)
	res := ontology.ContactsResult{Close: []ontology.Contact{}, Secondary: []ontology.Contact{}}
	if c.revoked {
		return res, nil
	}
	closeAcc, secondary := e.derive(c, now)
	for p, acc := range closeAcc {
		res.Close = append(res.Close, ontology.Contact{
			PatientID: p, Minutes: acc.minutes, LastContact: acc.last,
		})
	}
	for _, ct := range secondary {
		res.Secondary = append(res.Secondary, ct)
	}
	sort.Slice(res.Close, func(i, j int) bool { return res.Close[i].PatientID < res.Close[j].PatientID })
	sort.Slice(res.Secondary, func(i, j int) bool { return res.Secondary[i].PatientID < res.Secondary[j].PatientID })
	return res, nil
}
