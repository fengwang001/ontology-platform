package room

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/queue"
	"ontology/triage"
)

// ---- 朴素模型：每次操作前全量扫描落地、全量排序候选 ----

type naiveRoom struct {
	kind     Kind
	occupant string
	arrived  bool
}

type naivePatient struct {
	regno   int
	level   int
	q, la   int
	miss    int
	status  Status // 复用导出枚举（Waiting/Called/Gone/Finished）
	room    string
	callAt  int
	arrived bool
}

type naive struct {
	r      [5]int
	a      int
	rooms  map[string]*naiveRoom
	ps     map[string]*naivePatient
	maxNow int
	reg    int
}

func newNaive(r1, r2, r3, r4, a int) *naive {
	n := &naive{rooms: map[string]*naiveRoom{}, ps: map[string]*naivePatient{}, a: a}
	n.r[1], n.r[2], n.r[3], n.r[4] = r1, r2, r3, r4
	return n
}

func (n *naive) land(now int) {
	type dl struct {
		p *naivePatient
		d int
	}
	var list []dl
	for _, p := range n.ps {
		if p.status == Called && !p.arrived {
			list = append(list, dl{p, p.callAt + n.a})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].d != list[j].d {
			return list[i].d < list[j].d
		}
		return list[i].p.regno < list[j].p.regno
	})
	for _, x := range list {
		if x.d >= now {
			break
		}
		p := x.p
		n.rooms[p.room].occupant = ""
		n.rooms[p.room].arrived = false
		p.miss++
		if p.miss >= 3 {
			p.status = Gone
		} else {
			p.status = Waiting
			p.q = x.d
		}
		p.room = ""
	}
}

type naiveResult struct {
	regno, level int
	call         CallResult
	finish       string
	err          error
}

func statusMatch(s, t Status) bool { return s == t }

func (n *naive) do(op string, now int, p, rm string, v triage.Vitals) naiveResult {
	res := naiveResult{}
	valid := true
	switch op {
	case "Register":
		valid = now >= 0 && now <= 1e9 && p != "" && triage.ValidVitals(v)
	case "Call", "Finish":
		valid = now >= 0 && now <= 1e9 && rm != ""
	case "Reassess", "Arrive":
		valid = now >= 0 && now <= 1e9 && p != ""
	}
	if !valid {
		res.err = ErrInvalid
		return res
	}
	if now < n.maxNow {
		res.err = ErrClock
		return res
	}
	switch op {
	case "Register":
		if _, ok := n.ps[p]; ok {
			res.err = ErrNotFound
			return res
		}
	case "Reassess":
		if _, ok := n.ps[p]; !ok {
			res.err = ErrNotFound
			return res
		}
	case "Arrive":
		if _, ok := n.ps[p]; !ok {
			res.err = ErrNotFound
			return res
		}
	case "Call", "Finish":
		if _, ok := n.rooms[rm]; !ok {
			res.err = ErrNotFound
			return res
		}
	}
	// 状态前置（落地前视角）
	switch op {
	case "Reassess":
		if n.ps[p].status != Waiting {
			res.err = ErrState
			return res
		}
	case "Arrive":
		x := n.ps[p]
		if x.status != Called || x.arrived || x.callAt+n.a < now {
			res.err = ErrState
			return res
		}
	case "Call":
		if occ := n.rooms[rm].occupant; occ != "" {
			x := n.ps[occ]
			if x.arrived || x.callAt+n.a >= now {
				res.err = ErrState
				return res
			}
		}
	case "Finish":
		x := n.rooms[rm]
		if x.occupant == "" || !x.arrived {
			res.err = ErrState
			return res
		}
	}
	n.land(now)
	n.maxNow = now
	switch op {
	case "Register":
		n.reg++
		lv := triage.Level(v)
		n.ps[p] = &naivePatient{regno: n.reg, level: lv, q: now, la: now, status: Waiting}
		res.regno, res.level = n.reg, lv
	case "Reassess":
		x := n.ps[p]
		lv := triage.Level(v)
		x.la = now
		if lv < x.level {
		} else if lv > x.level {
			x.q = now
		}
		x.level = lv
		res.level = lv
	case "Call":
		r := n.rooms[rm]
		var levels []int
		if r.kind == Rescue {
			levels = []int{1, 2}
		} else {
			levels = []int{2, 3, 4}
		}
		type cand struct{ p *naivePatient }
		var all []*naivePatient
		for _, x := range n.ps {
			if x.status != Waiting {
				continue
			}
			ok := false
			for _, lv := range levels {
				if x.level == lv {
					ok = true
				}
			}
			if ok {
				all = append(all, x)
			}
		}
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].level != all[j].level {
				return all[i].level < all[j].level
			}
			if all[i].q != all[j].q {
				return all[i].q < all[j].q
			}
			return all[i].regno < all[j].regno
		})
		var skipped []string
		var pick *naivePatient
		for _, x := range all {
			if now-x.la > n.r[x.level] {
				skipped = append(skipped, nameOf(x, n.ps))
				continue
			}
			pick = x
			break
		}
		if pick == nil {
			res.err = ErrNoCallee
			return res
		}
		nm := nameOf(pick, n.ps)
		pick.status = Called
		pick.callAt = now
		pick.room = rm
		r.occupant = nm
		if skipped == nil {
			skipped = []string{}
		}
		res.call = CallResult{Patient: nm, Level: pick.level, Skipped: skipped}
	case "Arrive":
		x := n.ps[p]
		x.arrived = true
		n.rooms[x.room].arrived = true
	case "Finish":
		r := n.rooms[rm]
		x := n.ps[r.occupant]
		x.status = Finished
		res.finish = r.occupant
		r.occupant = ""
		r.arrived = false
	}
	return res
}

func nameOf(target *naivePatient, m map[string]*naivePatient) string {
	for k, v := range m {
		if v == target {
			return k
		}
	}
	return ""
}

func (n *naive) addRoom(rm string, k Kind) error {
	if rm == "" || (k != Rescue && k != Clinic) {
		return ErrInvalid
	}
	if _, ok := n.rooms[rm]; ok {
		return ErrNotFound
	}
	n.rooms[rm] = &naiveRoom{kind: k}
	return nil
}

type step struct {
	op   string
	now  int
	p, r string
	v    triage.Vitals
}

func randVitals(rng *rand.Rand) triage.Vitals {
	// 以 30% 概率从“各档边界点”抽取，提高边界覆盖。
	if rng.Intn(10) < 3 {
		hrs := []int{39, 40, 49, 50, 100, 101, 110, 111, 130, 131}
		sbps := []int{69, 70, 80, 81, 100, 101, 199, 200}
		sps := []int{84, 85, 89, 90, 93, 94}
		locs := []triage.Consciousness{triage.A, triage.V, triage.P, triage.U}
		return triage.Vitals{HR: hrs[rng.Intn(len(hrs))], SBP: sbps[rng.Intn(len(sbps))], SPO2: sps[rng.Intn(len(sps))], LOC: locs[rng.Intn(4)]}
	}
	return triage.Vitals{
		HR:   rng.Intn(301),
		SBP:  rng.Intn(301),
		SPO2: rng.Intn(101),
		LOC:  triage.Consciousness(rng.Intn(4)),
	}
}

func TestCompareNaive1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const groups = 1500
	rng := rand.New(rand.NewSource(20261005))
	for g := 0; g < groups; g++ {
		R := [4]int{1 + rng.Intn(6), 1 + rng.Intn(20), 1 + rng.Intn(40), 1 + rng.Intn(70)}
		A := 1 + rng.Intn(10)
		s := NewSystem(R[0], R[1], R[2], R[3], A)
		nv := newNaive(R[0], R[1], R[2], R[3], A)
		var rooms []string
		nRooms := 1 + rng.Intn(4)
		for i := 0; i < nRooms; i++ {
			rid := fmt.Sprintf("rm%d", i)
			k := Kind(rng.Intn(2))
			s.AddRoom(RoomID(rid), k)
			nv.addRoom(rid, k)
			rooms = append(rooms, rid)
		}
		var patients []string
		nP := 4 + rng.Intn(10)
		for i := 0; i < nP; i++ {
			patients = append(patients, fmt.Sprintf("p%d", i))
		}
		var log []step
		now := 0
		nSteps := 30 + rng.Intn(40)
		// 预置：先登记全部患者。
		for _, p := range patients {
			v := randVitals(rng)
			now += rng.Intn(3)
			st := step{"Register", now, p, "", v}
			log = append(log, st)
			_, _, e1 := s.Register(now, p, v)
			r2 := nv.do("Register", now, p, "", v)
			if diffErr(e1, r2.err) {
				t.Fatalf("group %d register mismatch real=%v naive=%v; input=%+v", g, e1, r2.err, st)
			}
		}
		for i := 0; i < nSteps; i++ {
			op := []string{"Reassess", "Call", "Arrive", "Finish"}[rng.Intn(4)]
			now += rng.Intn(4)
			p := patients[rng.Intn(len(patients))]
			rm := rooms[rng.Intn(len(rooms))]
			v := randVitals(rng)
			st := step{op, now, p, rm, v}
			log = append(log, st)

			var realErr error
			var realCall CallResult
			var realLv int
			var realFin string
			switch op {
			case "Reassess":
				realLv, realErr = s.Reassess(now, p, v)
			case "Call":
				realCall, realErr = s.Call(now, RoomID(rm))
			case "Arrive":
				realErr = s.Arrive(now, p)
			case "Finish":
				realFin, realErr = s.Finish(now, RoomID(rm))
			}
			nr := nv.do(op, now, p, rm, v)
			why := fmt.Sprintf("group=%d step=%d/%d op=%+v", g, i, nSteps, st)
			if diffErr(realErr, nr.err) {
				dumpLog(t, log)
				t.Fatalf("err mismatch real=%v naive=%v; %s", realErr, nr.err, why)
			}
			if realErr == nil {
				switch op {
				case "Reassess":
					if realLv != nr.level {
						t.Fatalf("level %d!=%d; %s", realLv, nr.level, why)
					}
				case "Call":
					if realCall.Patient != nr.call.Patient || realCall.Level != nr.call.Level ||
						!sliceEq(realCall.Skipped, nr.call.Skipped) {
						dumpLog(t, log)
						t.Fatalf("call real=%+v naive=%+v; %s", realCall, nr.call, why)
					}
				case "Finish":
					if realFin != nr.finish {
						t.Fatalf("finish %s!=%s; %s", realFin, nr.finish, why)
					}
				}
			}
			// 状态快照对照。
			s.mu.Lock()
			for _, pp := range patients {
				re, ok := s.qq.Get(queue.ID(pp))
				ne := nv.ps[pp]
				if !ok {
					t.Fatalf("real missing %s; %s", pp, why)
				}
				if re.Level != ne.level || re.Q != ne.q || re.LA != ne.la ||
					re.Miss != ne.miss || int(re.Status) != int(ne.status) {
					dumpLog(t, log)
					t.Fatalf("state %s real{lv=%d q=%d la=%d miss=%d st=%d} naive{lv=%d q=%d la=%d miss=%d st=%d}; %s",
						pp, re.Level, re.Q, re.LA, re.Miss, re.Status,
						ne.level, ne.q, ne.la, ne.miss, ne.status, why)
				}
			}
			s.mu.Unlock()
		}
		if g < 5 || g == groups-1 {
			t.Logf("组 %d: R=%v A=%d 房间=%d 患者=%d 步骤=%d 全部一致 (输入/输出/状态见判定断言)", g, R, A, nRooms, nP, len(log))
		}
	}
}

func diffErr(a, b error) bool {
	return errorCode(a) != errorCode(b)
}

func errorCode(e error) int {
	switch {
	case e == nil:
		return 0
	case errorsIs(e, ErrInvalid):
		return 1
	case errorsIs(e, ErrClock):
		return 2
	case errorsIs(e, ErrNotFound):
		return 3
	case errorsIs(e, ErrState):
		return 4
	case errorsIs(e, ErrNoCallee):
		return 5
	}
	return -1
}

func errorsIs(err, target error) bool {
	return err == target
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dumpLog(t *testing.T, log []step) {
	for i, st := range log {
		t.Logf("输入[%d] op=%s now=%d patient=%s room=%s vitals={hr=%d sbp=%d spo2=%d loc=%d}",
			i, st.op, st.now, st.p, st.r, st.v.HR, st.v.SBP, st.v.SPO2, st.v.LOC)
	}
}
