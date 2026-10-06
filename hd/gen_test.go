package hd

import "math/rand"

// generateOps 生成一条结构受限但内容随机的操作序列：
// - now 单调不减（偶尔回退以触发时钟错误）；
// - 对象引用来自已登记集合，也有少量幽灵引用触发 NOT_FOUND；
// - 治疗时间集中在有限跨度内，使消毒/恢复/深度消毒冲突高频出现；
// - 故障、恢复、状态变更、取消交错，覆盖改派与重核路径。
func generateOps(r *rand.Rand) []op {
	const nBays = 6
	const nPatients = 5
	const nSteps = 60

	_ = nBays
	_ = nPatients
	// 机位编号：G1..G4（普通，其中 G3 观察位），I1..I2（隔离）。
	general := []string{"G1", "G2", "G3", "G4"}
	isolation := []string{"I1", "I2"}
	allBays := append(append([]string{}, general...), isolation...)
	zoneOf := map[string]Zone{}
	patients := []string{"p1", "p2", "p3", "p4", "p5"}
	infs := []Infection{InfectionNegative, InfectionHBV, InfectionHCV, InfectionPending}

	var ops []op
	now := 0
	advance := func(maxStep int) int {
		if r.Intn(8) == 0 {
			return now - r.Intn(5) - 1 // 故意回退
		}
		now += r.Intn(maxStep + 1)
		return now
	}

	// 登记全部机位。
	for _, id := range allBays {
		z := ZoneGeneral
		obs := id == "G3"
		if id[0] == 'I' {
			z = ZoneIsolation
		}
		zoneOf[id] = z
		now++
		ops = append(ops, op{kind: opRegBay, now: now, id: id, zone: z, observed: obs})
	}
	// 登记全部患者。
	for i, id := range patients {
		now++
		ops = append(ops, op{kind: opRegPatient, now: now, id: id, inf: infs[i%len(infs)]})
	}

	booked := []string{}
	plans := []string{}
	seq := 0
	newTID := func(prefix string) string {
		seq++
		return prefix + "-" + itoa(seq)
	}

	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }

	for step := 0; step < nSteps; step++ {
		kind := opKind(r.Intn(int(opChange) + 1))
		switch kind {
		case opRegBay, opRegPatient:
			// 低概率重复登记，制造 INVALID_STATE。
			if kind == opRegBay && r.Intn(3) == 0 {
				now++
				id := pick(allBays)
				ops = append(ops, op{kind: opRegBay, now: now, id: id,
					zone: zoneOf[id], observed: id == "G3"})
			}
		case opBook:
			tid := newTID("t")
			pid := pick(patients)
			if r.Intn(10) == 0 {
				pid = "ghost"
			}
			n := advance(200)
			start := 100 + r.Intn(4000)
			dur := 20 + r.Intn(180)
			o := op{kind: opBook, now: n, id: tid, pid: pid, start: start, dur: dur}
			ops = append(ops, o)
			booked = append(booked, tid)
		case opApplyPlan:
			pid := pick(patients)
			planID := newTID("plan")
			n := advance(100)
			days := map[int]bool{}
			for d := 0; d < 7; d++ {
				if r.Intn(2) == 0 {
					days[d] = true
				}
			}
			if len(days) == 0 {
				days[r.Intn(7)] = true
			}
			dayStart := 500 + r.Intn(500)
			dur := 30 + r.Intn(120)
			fromDay := r.Intn(3)
			toDay := fromDay + r.Intn(9)
			o := op{
				kind: opApplyPlan, now: n, id: planID, pid: pid, days: days,
				dayStart: dayStart, dur: dur,
				from: fromDay * minutesPerDay,
				to:   toDay * minutesPerDay,
			}
			ops = append(ops, o)
			plans = append(plans, planID)
		case opCancel:
			n := advance(200)
			if len(booked) > 0 && r.Intn(2) == 0 {
				tid := pick(booked)
				ops = append(ops, op{kind: opCancel, now: n, id: tid})
			} else {
				ops = append(ops, op{kind: opCancel, now: n, id: "ghost-t"})
			}
		case opCancelPlan:
			n := advance(200)
			if len(plans) > 0 && r.Intn(2) == 0 {
				ops = append(ops, op{kind: opCancelPlan, now: n, id: pick(plans)})
			} else {
				ops = append(ops, op{kind: opCancelPlan, now: n, id: "ghost-plan"})
			}
		case opFault:
			n := advance(300)
			bay := pick(allBays)
			ops = append(ops, op{kind: opFault, now: n, id: bay, faultAt: 100 + r.Intn(3500)})
		case opRecover:
			n := advance(300)
			bay := pick(allBays)
			ops = append(ops, op{kind: opRecover, now: n, id: bay, recoverAt: 100 + r.Intn(4000)})
		case opChange:
			n := advance(200)
			pid := pick(patients)
			newInf := infs[r.Intn(3)] // 不含待定
			ops = append(ops, op{kind: opChange, now: n, pid: pid, inf: newInf,
				changeAt: r.Intn(3000)})
		}
	}
	return ops
}
