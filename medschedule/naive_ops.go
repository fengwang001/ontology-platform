package medschedule

import "sort"

func (m *NaiveModel) StopOrder(now int64, id string) error {
	if !validNow(now) || id == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return errNotFound("no order")
	}
	if o.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	m.advance(now)
	o.stoppedAt = now
	return nil
}

func (m *NaiveModel) ReplaceOrder(now int64, oldID string, spec Spec) error {
	if !m.validSpec(spec, now) || oldID == "" || spec.ID == oldID {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	old, ok := m.orders[oldID]
	if !ok {
		return errNotFound("no order")
	}
	if old.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	d, exists := m.drugs[spec.Drug]
	if !exists {
		return errNotFound("no drug")
	}
	if _, dup := m.orders[spec.ID]; dup {
		return errBadState("dup")
	}
	if set := m.allergy[spec.Patient]; set != nil && (set[spec.Drug] || set[d.Category]) {
		return errAllergy("allergy")
	}
	if !nvSpacing(spec.Kind, spec.H, spec.TimesOfDay, m.w, d.MinIntervalSec) {
		return errInvalid("spacing")
	}
	m.advance(now)
	old.stoppedAt = now
	m.orders[spec.ID] = m.buildOrder(spec, now)
	return nil
}

func (m *NaiveModel) findOnTime(o *nvOrder, now int64) *nvPoint {
	for i := range o.points {
		p := &o.points[i]
		if !p.alive || p.status != nvPending {
			continue
		}
		if p.t-m.w <= now && now <= p.t+m.w {
			return p
		}
	}
	return nil
}

// findMakeup 返回当前可补给点与下一点时刻（hasNxt 为是否存在）。
func (m *NaiveModel) findMakeup(o *nvOrder, now int64) (*nvPoint, int64, bool) {
	var target *nvPoint
	var nxtT int64
	hasNxt := false
	for i := range o.points {
		p := &o.points[i]
		if !p.alive || p.status != nvPending {
			continue
		}
		if now > p.t+m.w {
			target = p
			hasNxt = false
			// 重排后活点与旧网格死点可能交错，向后找第一个活点作为下一点。
			for j := i + 1; j < len(o.points); j++ {
				if o.points[j].alive {
					nxtT = o.points[j].t
					hasNxt = true
					break
				}
			}
		}
	}
	if target == nil {
		return nil, 0, false
	}
	if hasNxt && now >= nxtT-m.w {
		return nil, 0, false
	}
	return target, nxtT, hasNxt
}

func (m *NaiveModel) safetyGap(patient, drug string, now int64) error {
	d, ok := m.drugs[drug]
	if !ok {
		return errNotFound("drug")
	}
	if last, ok := m.lastAdm[patientDrugKey(patient, drug)]; ok && now-last < d.MinIntervalSec {
		return errInterval("gap")
	}
	return nil
}

func (m *NaiveModel) Administer(now int64, id string) error {
	if !validNow(now) || id == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return errNotFound("no order")
	}
	if o.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	if o.kind == "prn" {
		return errBadState("prn")
	}
	m.materializeAll(now)
	p := m.findOnTime(o, now)
	if p == nil {
		return errNoPoint("no point")
	}
	if err := m.safetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	p.status = nvGiven
	m.lastAdm[patientDrugKey(o.patient, o.drug)] = now
	m.now = now
	return nil
}

func (m *NaiveModel) Refuse(now int64, id string) error {
	if !validNow(now) || id == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return errNotFound("no order")
	}
	if o.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	if o.kind == "prn" {
		return errBadState("prn")
	}
	m.materializeAll(now)
	p := m.findOnTime(o, now)
	if p == nil {
		return errNoPoint("no point")
	}
	p.status = nvRefused
	m.now = now
	return nil
}

func (m *NaiveModel) MakeUp(now int64, id string) error {
	if !validNow(now) || id == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return errNotFound("no order")
	}
	if o.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	if o.kind == "prn" {
		return errBadState("prn")
	}
	m.materializeAll(now)
	p, nxtT, hasNxt := m.findMakeup(o, now)
	if p == nil {
		return errNoPoint("no makeable")
	}
	if err := m.safetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	if hasNxt && now >= nxtT-m.w {
		return errMakeup("not allowed")
	}
	p.status = nvMadeUp
	m.lastAdm[patientDrugKey(o.patient, o.drug)] = now
	if o.kind == "interval" {
		// 截断语义（与高效实现对齐）：
		//   - (p, newStart) 内的旧点保留为作废（被替换掉的那一段尾巴）；
		//   - newStart 及以后的旧网格点不再属于当前时间表，直接删除，
		//     该时间区域由从 newStart 起的新网格接管。
		newStart := now + o.h
		// 作废尾巴可能越过当前 now+W，先把旧网格物化到新段起点之前。
		m.materialize(o, newStart-1)
		kept := make([]nvPoint, 0, len(o.points))
		for i := range o.points {
			q := &o.points[i]
			if q.alive && q.status == nvPending && q.t > p.t && q.t < newStart {
				qq := *q
				qq.alive = false
				kept = append(kept, qq)
			} else if q.alive && q.status == nvPending && q.t >= newStart {
				continue // 新网格接管，旧点删除
			} else {
				kept = append(kept, *q)
			}
		}
		o.points = kept
		// 重置网格锚点，由 materialize 统一物化到当前视界。
		o.gridStart = newStart
		o.genUpto = -1
		m.materialize(o, now)
		sort.Slice(o.points, func(i, j int) bool { return o.points[i].t < o.points[j].t })
	}
	m.now = now
	return nil
}

func (m *NaiveModel) AdministerPRN(now int64, id string) error {
	if !validNow(now) || id == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return errNotFound("no order")
	}
	if o.stoppedAt >= 0 {
		return errBadState("stopped")
	}
	if o.kind != "prn" {
		return errBadState("not prn")
	}
	if n := len(o.prnDoses); n > 0 && now-o.prnDoses[n-1] < o.prnGap {
		return errInterval("prn gap")
	}
	if err := m.safetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	cutoff := now - dayLen
	count := 0
	for _, t := range o.prnDoses {
		if t > cutoff {
			count++
		}
	}
	if int64(count) >= o.prnMax {
		return errPRNLimit("limit")
	}
	o.prnDoses = append(o.prnDoses, now)
	m.lastAdm[patientDrugKey(o.patient, o.drug)] = now
	m.now = now
	return nil
}

// NaiveQuery 返回朴素模型的区间点（状态含漏给/作废的即时判定）。
func (m *NaiveModel) NaiveQuery(now int64, patient string, lo, hi int64) []Point {
	// 查询只读：物化但不推进时钟。视界必须覆盖区间上界，
	// 否则未来点尚未落地；状态仍由查询时刻 now 判定。
	for _, o := range m.orders {
		m.materialize(o, hi)
	}
	var out []Point
	for _, o := range m.orders {
		if o.patient != patient || o.kind == "prn" {
			continue
		}
		for _, p := range o.points {
			if p.t < lo || p.t > hi {
				continue
			}
			var st Status
			switch {
			case !p.alive:
				st = StatusVoid
			case p.status == nvGiven:
				st = StatusGivenOnTime
			case p.status == nvMadeUp:
				st = StatusMadeUp
			case p.status == nvRefused:
				st = StatusRefused
			case o.stoppedAt >= 0:
				if p.t+m.w < o.stoppedAt {
					st = StatusMissed
				} else {
					st = StatusVoid
				}
			case now > p.t+m.w:
				st = StatusMissed
			default:
				st = StatusPending
			}
			out = append(out, Point{OrderID: o.id, Patient: o.patient, Drug: o.drug, Time: p.t, Status: st})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		if out[i].OrderID != out[j].OrderID {
			return out[i].OrderID < out[j].OrderID
		}
		return out[i].Status < out[j].Status
	})
	return out
}

// NaivePRN 返回朴素模型的 PRN 给药记录。
func (m *NaiveModel) NaivePRN(patient string) []PRNDose {
	var out []PRNDose
	for _, o := range m.orders {
		if o.patient != patient || o.kind != "prn" {
			continue
		}
		for _, t := range o.prnDoses {
			out = append(out, PRNDose{OrderID: o.id, Patient: o.patient, Drug: o.drug, Time: t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].OrderID < out[j].OrderID
	})
	return out
}
