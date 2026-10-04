package transfer

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/ward"
)

// 朴素参考模型：独立数据结构，每次操作逐床重扫，不共享实现中的任何代码。

type nBed struct {
	occupant   string
	reservedBy string
	reserveAt  int64
	reserveSex int
	reserveIso bool
	cleanUntil int64
}

type nPatient struct {
	sex        ward.Sex
	iso        bool
	ward, room string
	bed        int
	to, toR    string
	toB        int
	reqAt      int64
	hasT       bool
}

type naive struct {
	cl, hold  int64
	rooms     map[string]map[string]map[int]*nBed
	patients  map[string]*nPatient
	grants    map[string]map[string]bool
	lastNow   int64
	totalBeds map[string]int
}

func newNaive(cl, hold int) *naive {
	return &naive{
		cl: int64(cl), hold: int64(hold),
		rooms:     map[string]map[string]map[int]*nBed{},
		patients:  map[string]*nPatient{},
		grants:    map[string]map[string]bool{},
		totalBeds: map[string]int{},
	}
}

func (n *naive) addRoom(wn, rn string, beds int) {
	if n.rooms[wn] == nil {
		n.rooms[wn] = map[string]map[int]*nBed{}
	}
	m := map[int]*nBed{}
	for i := 1; i <= beds; i++ {
		m[i] = &nBed{}
	}
	n.rooms[wn][rn] = m
	n.totalBeds[wn] += beds
}

func (n *naive) grant(op, wn string) {
	if n.grants[wn] == nil {
		n.grants[wn] = map[string]bool{}
	}
	n.grants[wn][op] = true
}

func (b *nBed) state(now, hold int64) ward.BedState {
	if b.occupant != "" {
		return ward.BedOccupied
	}
	if b.reservedBy != "" && now < b.reserveAt+hold {
		return ward.BedReserved
	}
	if now < b.cleanUntil {
		return ward.BedCleaning
	}
	return ward.BedFree
}

func (n *naive) sortedRooms(wn string) []string {
	rs := make([]string, 0, len(n.rooms[wn]))
	for r := range n.rooms[wn] {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	return rs
}

func sortedNums(m map[int]*nBed) []int {
	bs := make([]int, 0, len(m))
	for k := range m {
		bs = append(bs, k)
	}
	sort.Ints(bs)
	return bs
}

// choose：逐床扫描的朴素选床。
func (n *naive) choose(now int64, wn string, sex ward.Sex, iso bool) (string, int, int) {
	examined := 0
	if iso {
		bestR, bestN := "", 0
		for _, rn := range n.sortedRooms(wn) {
			beds := n.rooms[wn][rn]
			allFree := true
			for _, bn := range sortedNums(beds) {
				examined++
				if beds[bn].state(now, n.hold) != ward.BedFree {
					allFree = false
				}
			}
			if allFree && (bestR == "" || len(beds) < bestN) {
				bestR, bestN = rn, len(beds)
			}
		}
		if bestR == "" {
			return "", 0, examined
		}
		return bestR, sortedNums(n.rooms[wn][bestR])[0], examined
	}

	bestOcc, bestFree := "", 0
	bestEmpty, bestTot := "", 0
	for _, rn := range n.sortedRooms(wn) {
		beds := n.rooms[wn][rn]
		free, occ := 0, 0
		same, flagged := true, false
		for _, bn := range sortedNums(beds) {
			b := beds[bn]
			examined++
			switch b.state(now, n.hold) {
			case ward.BedFree:
				free++
			case ward.BedOccupied:
				occ++
				p := n.patients[b.occupant]
				if p.sex != sex {
					same = false
				}
				if p.iso {
					flagged = true
				}
			case ward.BedReserved:
				occ++
				if b.reserveSex != int(sex) {
					same = false
				}
				if b.reserveIso {
					flagged = true
				}
			}
		}
		if flagged || free == 0 || !same {
			continue
		}
		if occ > 0 {
			if bestOcc == "" || free < bestFree {
				bestOcc, bestFree = rn, free
			}
		} else if bestEmpty == "" || len(beds) < bestTot {
			bestEmpty, bestTot = rn, len(beds)
		}
	}
	rn := bestOcc
	if rn == "" {
		rn = bestEmpty
	}
	if rn == "" {
		return "", 0, examined
	}
	for _, bn := range sortedNums(n.rooms[wn][rn]) {
		if n.rooms[wn][rn][bn].state(now, n.hold) == ward.BedFree {
			return rn, bn, examined
		}
	}
	return "", 0, examined
}

type nOp struct {
	kind              string
	now               int64
	op, pat, to, ward string
	sex               ward.Sex
	iso, on           bool
}

func validN(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

// apply 返回 (errKind, room, bed)。
func (n *naive) apply(o nOp) (string, string, int) {
	invalid := func() (string, string, int) { return "invalid", "", 0 }
	switch o.kind {
	case "admit":
		if !validN(o.now) || o.op == "" || o.pat == "" ||
			(o.sex != ward.SexMale && o.sex != ward.SexFemale) || o.ward == "" {
			return invalid()
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		if n.rooms[o.ward] == nil {
			return "notfound", "", 0
		}
		if !n.grants[o.ward][o.op] {
			return "nogrant", "", 0
		}
		if _, ok := n.patients[o.pat]; ok {
			return "state", "", 0
		}
		rn, bn, _ := n.choose(o.now, o.ward, o.sex, o.iso)
		if rn == "" {
			return "nobed", "", 0
		}
		b := n.rooms[o.ward][rn][bn]
		if old := b.reservedBy; old != "" {
			if p := n.patients[old]; p != nil {
				p.hasT = false
			}
		}
		b.reservedBy, b.reserveAt, b.reserveSex, b.reserveIso = "", 0, 0, false
		b.occupant = o.pat
		n.patients[o.pat] = &nPatient{sex: o.sex, iso: o.iso, ward: o.ward, room: rn, bed: bn}
		n.lastNow = o.now
		return "", rn, bn
	case "discharge":
		if !validN(o.now) || o.op == "" || o.pat == "" {
			return invalid()
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		p, ok := n.patients[o.pat]
		if !ok {
			return "notfound", "", 0
		}
		if !n.grants[p.ward][o.op] {
			return "nogrant", "", 0
		}
		n.dropTransfer(p, o.now)
		n.rooms[p.ward][p.room][p.bed].occupant = ""
		n.rooms[p.ward][p.room][p.bed].cleanUntil = o.now + n.cl
		delete(n.patients, o.pat)
		n.lastNow = o.now
		return "", "", 0
	case "request":
		if !validN(o.now) || o.op == "" || o.pat == "" || o.to == "" {
			return invalid()
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		p, ok := n.patients[o.pat]
		if !ok || n.rooms[o.to] == nil {
			return "notfound", "", 0
		}
		if o.to == p.ward {
			return "invalid", "", 0
		}
		if !n.grants[p.ward][o.op] {
			return "nogrant", "", 0
		}
		if p.hasT && o.now < p.reqAt+n.hold {
			return "state", "", 0
		}
		rn, bn, _ := n.choose(o.now, o.to, p.sex, p.iso)
		if rn == "" {
			return "nobed", "", 0
		}
		b := n.rooms[o.to][rn][bn]
		b.reservedBy, b.reserveAt = o.pat, o.now
		b.reserveSex, b.reserveIso = int(p.sex), p.iso
		p.hasT, p.to, p.toR, p.toB, p.reqAt = true, o.to, rn, bn, o.now
		n.lastNow = o.now
		return "", rn, bn
	}
	return n.applyRest(o)
}

func (n *naive) dropTransfer(p *nPatient, now int64) {
	if !(p.hasT && now < p.reqAt+n.hold) {
		p.hasT = false
		return
	}
	b := n.rooms[p.to][p.toR][p.toB]
	b.reservedBy, b.reserveAt, b.reserveSex, b.reserveIso = "", 0, 0, false
	p.hasT = false
}

func (n *naive) applyRest(o nOp) (string, string, int) {
	switch o.kind {
	case "confirm":
		if !validN(o.now) || o.op == "" || o.pat == "" {
			return "invalid", "", 0
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		p, ok := n.patients[o.pat]
		if !ok {
			return "notfound", "", 0
		}
		if !p.hasT || o.now >= p.reqAt+n.hold {
			return "state", "", 0
		}
		if !n.grants[p.to][o.op] {
			return "nogrant", "", 0
		}
		old := n.rooms[p.ward][p.room][p.bed]
		nb := n.rooms[p.to][p.toR][p.toB]
		nb.reservedBy, nb.reserveAt, nb.reserveSex, nb.reserveIso = "", 0, 0, false
		nb.occupant = o.pat
		old.occupant = ""
		old.cleanUntil = o.now + n.cl
		p.ward, p.room, p.bed, p.hasT = p.to, p.toR, p.toB, false
		n.lastNow = o.now
		return "", "", 0
	case "cancel":
		if !validN(o.now) || o.op == "" || o.pat == "" {
			return "invalid", "", 0
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		p, ok := n.patients[o.pat]
		if !ok {
			return "notfound", "", 0
		}
		if !p.hasT || o.now >= p.reqAt+n.hold {
			return "state", "", 0
		}
		if !n.grants[p.ward][o.op] && !n.grants[p.to][o.op] {
			return "nogrant", "", 0
		}
		b := n.rooms[p.to][p.toR][p.toB]
		b.reservedBy, b.reserveAt, b.reserveSex, b.reserveIso = "", 0, 0, false
		p.hasT = false
		n.lastNow = o.now
		return "", "", 0
	case "setiso":
		if !validN(o.now) || o.op == "" || o.pat == "" {
			return "invalid", "", 0
		}
		if o.now < n.lastNow {
			return "clock", "", 0
		}
		p, ok := n.patients[o.pat]
		if !ok {
			return "notfound", "", 0
		}
		if !n.grants[p.ward][o.op] {
			return "nogrant", "", 0
		}
		if p.hasT && o.now < p.reqAt+n.hold {
			return "state", "", 0
		}
		if o.on {
			for _, bn := range sortedNums(n.rooms[p.ward][p.room]) {
				b := n.rooms[p.ward][p.room][bn]
				st := b.state(o.now, n.hold)
				if (st == ward.BedOccupied || st == ward.BedReserved) &&
					b.occupant != o.pat && b.reservedBy != o.pat {
					return "notsole", "", 0
				}
			}
		}
		p.iso = o.on
		n.lastNow = o.now
		return "", "", 0
	}
	return "invalid", "", 0
}

var errKind = map[error]string{
	ward.ErrInvalid:  "invalid",
	ward.ErrClock:    "clock",
	ward.ErrNotFound: "notfound",
	ward.ErrNoGrant:  "nogrant",
	ward.ErrState:    "state",
	ward.ErrNoBed:    "nobed",
	ward.ErrNotSole:  "notsole",
}

func kindOf(err error) string {
	if err == nil {
		return ""
	}
	for e, k := range errKind {
		if err == e {
			return k
		}
	}
	return "??"
}

// assertInvariants 检查实现状态满足全部全局不变量。
func assertInvariants(t *testing.T, h *ward.Hospital, now int64, tag string) {
	t.Helper()
	bedOwners := map[string]string{}
	for pid, p := range h.Patients {
		occKey := p.Ward + "/" + p.Room + "/" + nitoa(p.Bed)
		if prev, ok := bedOwners[occKey]; ok {
			t.Fatalf("[%s] bed %s double-occupied by %s,%s", tag, occKey, prev, pid)
		}
		bedOwners[occKey] = pid
		// 同一房间在房者性别一致（含预留），且带隔离标志时恰一名在房者。
		sexes := map[ward.Sex]bool{}
		present := 0
		isoFlag := false
		for _, b := range h.Wards[p.Ward].Rooms[p.Room].OrderedBeds() {
			st := b.State(now, h.H)
			if st == ward.BedOccupied {
				q := h.Patients[b.Occupant]
				sexes[q.Sex] = true
				present++
				if q.Iso {
					isoFlag = true
				}
			} else if st == ward.BedReserved {
				sexes[b.ReserveSex] = true
				present++
				if b.ReserveIso {
					isoFlag = true
				}
			}
		}
		if len(sexes) > 1 {
			t.Fatalf("[%s] room occupants mixed sex", tag)
		}
		if isoFlag && present != 1 {
			t.Fatalf("[%s] iso-flagged room has %d present", tag, present)
		}
	}
	// 有效预留的归属一致性。
	for pid, p := range h.Patients {
		if p.Transfer == nil || now >= p.Transfer.CreatedAt+h.H {
			continue
		}
		b := h.Wards[p.Transfer.ToWard].Rooms[p.Transfer.ToRoom].Beds[p.Transfer.ToBed]
		if b.ReservedBy != pid {
			t.Fatalf("[%s] reserve bed not held by %s", tag, pid)
		}
	}
}

// snapshotStates 汇总实现与朴素模型中每张床的派生状态与在房者，供逐项对账。
func snapshotStates(t *testing.T, h *ward.Hospital, n *naive, now int64) {
	t.Helper()
	for wn, rooms := range n.rooms {
		for rn, beds := range rooms {
			for bn, nb := range beds {
				ib := h.Wards[wn].Rooms[rn].Beds[bn]
				is := ib.State(now, h.H)
				ns := nb.state(now, n.hold)
				if int(is) != int(ns) {
					t.Fatalf("state mismatch %s/%s/%d: impl=%d naive=%d", wn, rn, bn, is, ns)
				}
				if is == ward.BedOccupied && ib.Occupant != nb.occupant {
					t.Fatalf("occupant mismatch %s/%s/%d: %s vs %s", wn, rn, bn, ib.Occupant, nb.occupant)
				}
				if is == ward.BedReserved && ib.ReservedBy != nb.reservedBy {
					t.Fatalf("reserver mismatch %s/%s/%d: %s vs %s", wn, rn, bn, ib.ReservedBy, nb.reservedBy)
				}
			}
		}
	}
	for pid, np := range n.patients {
		ip, ok := h.Patients[pid]
		if !ok {
			t.Fatalf("patient %s missing in impl", pid)
		}
		if ip.Ward != np.ward || ip.Room != np.room || ip.Bed != np.bed ||
			ip.Sex != np.sex || ip.Iso != np.iso {
			t.Fatalf("patient %s mismatch: impl=%+v naive=%+v", pid, ip, np)
		}
		implT := ip.Transfer != nil && now < ip.Transfer.CreatedAt+h.H
		naiveT := np.hasT && now < np.reqAt+n.hold
		if implT != naiveT {
			t.Fatalf("patient %s transfer validity mismatch", pid)
		}
		if implT && (ip.Transfer.ToWard != np.to || ip.Transfer.ToRoom != np.toR ||
			ip.Transfer.ToBed != np.toB) {
			t.Fatalf("patient %s transfer target mismatch", pid)
		}
	}
	if len(h.Patients) != len(n.patients) {
		t.Fatalf("patient count mismatch %d vs %d", len(h.Patients), len(n.patients))
	}
}

func TestRandomDifferential(t *testing.T) {
	const seqN = 1500
	for _, scale := range []struct {
		name  string
		wards int
	}{{"1ward", 1}, {"1000wards", 1000}} {
		t.Run(scale.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(1412 + scale.wards)))
			totalSeq := seqN
			if scale.wards > 1 {
				totalSeq = 30
			}
			for seq := 0; seq < totalSeq; seq++ {
				h, err := ward.New(1+rng.Intn(40), 1+rng.Intn(80))
				if err != nil {
					t.Fatal(err)
				}
				nv := newNaive(int(h.Cl), int(h.H))
				s := New(h)
				nW := scale.wards
				wardNames := make([]string, nW)
				for i := range wardNames {
					wardNames[i] = "W" + nitoa(i)
					rooms := 1
					if scale.wards == 1 {
						rooms = 1 + rng.Intn(3)
					}
					for r := 0; r < rooms; r++ {
						beds := 1 + rng.Intn(8)
						rn := "R" + nitoa(r)
						if e := h.AddRoom(wardNames[i], rn, beds); e != nil {
							t.Fatal(e)
						}
						nv.addRoom(wardNames[i], rn, beds)
					}
				}
				ops := []string{"op", "g2", "g3", "nobody"}
				h.Grant("op", wardNames[0])
				nv.grant("op", wardNames[0])
				if rng.Intn(2) == 0 {
					h.Grant("g2", wardNames[0])
					nv.grant("g2", wardNames[0])
				}
				if nW > 1 && rng.Intn(2) == 0 {
					h.Grant("op", wardNames[nW-1])
					nv.grant("op", wardNames[nW-1])
				}
				grantN := 8
				if nW < grantN {
					grantN = nW
				}
				for i := 0; i < grantN; i++ {
					wn := wardNames[rng.Intn(nW)]
					h.Grant("op", wn)
					nv.grant("op", wn)
				}

				var log []string
				now := int64(0)
				for step := 0; step < 40; step++ {
					o := genOp(rng, wardNames, ops, now)
					now = o.now
					var ik string
					var ir, ib2 string
					var ibed int
					switch o.kind {
					case "admit":
						c, e := s.Admit(o.now, o.op, o.pat, o.sex, o.iso, o.ward)
						ik = kindOf(e)
						if c != nil {
							ir, ibed = c.Room.Name, c.Bed.No
						}
					case "discharge":
						ik = kindOf(s.Discharge(o.now, o.op, o.pat))
					case "request":
						c, e := s.Request(o.now, o.op, o.pat, o.to)
						ik = kindOf(e)
						if c != nil {
							ir, ibed = c.Room.Name, c.Bed.No
						}
					case "confirm":
						ik = kindOf(s.Confirm(o.now, o.op, o.pat))
					case "cancel":
						ik = kindOf(s.CancelTransfer(o.now, o.op, o.pat))
					case "setiso":
						ik = kindOf(s.SetIso(o.now, o.op, o.pat, o.on))
					}
					nk, nr, nbed := nv.apply(o)
					ib2 = ""
					if ir != "" {
						ib2 = ir + "-" + nitoa(ibed)
					}
					nbStr := ""
					if nr != "" {
						nbStr = nr + "-" + nitoa(nbed)
					}
					log = append(log, fmtLog(o, ik, ib2, nk, nbStr))
					if ik != nk || ib2 != nbStr {
						for _, l := range log {
							t.Log(l)
						}
						t.Fatalf("seq %d step %d divergence: impl(%s,%s) naive(%s,%s)",
							seq, step, ik, ib2, nk, nbStr)
					}
					// 计数器界：目标病区床位数（Admit/Request 成功或无床判定）。
					if (o.kind == "admit" || o.kind == "request") && (ik == "" || ik == "nobed") {
						tw := o.ward
						if o.kind == "request" {
							tw = o.to
						}
						if tot := nv.totalBeds[tw]; tot > 0 && s.Examined() > tot {
							t.Fatalf("examined %d > target ward beds %d", s.Examined(), tot)
						}
					}
					if o.kind == "discharge" || o.kind == "confirm" {
						if s.Touched() > 2 {
							t.Fatalf("touched %d > 2", s.Touched())
						}
					}
					snapshotStates(t, h, nv, now)
					assertInvariants(t, h, now, fmt.Sprintf("seq%d-step%d", seq, step))
				}
				// 仅在前几条序列打印完整日志，避免噪声；失败时已打印。
				if seq < 2 {
					t.Logf("=== scale=%s seq=%d (%d ops) ===", scale.name, seq, len(log))
					for _, l := range log {
						t.Log(l)
					}
				}
			}
		})
	}
}

func genOp(rng *rand.Rand, wards, ops []string, now int64) nOp {
	// 时间：大部分单调，偶发回退以触发时钟拒绝；小步长以制造清洁/预留边界。
	next := now
	switch rng.Intn(10) {
	case 0:
		next = now - int64(rng.Intn(5))
		if next < 0 {
			next = 0
		}
	case 1:
		next = now
	default:
		next = now + int64(rng.Intn(30))
	}
	o := nOp{now: next, op: ops[rng.Intn(len(ops))]}
	switch k := rng.Intn(7); k {
	case 0, 1:
		o.kind = "admit"
		o.pat = "p" + nitoa(rng.Intn(12))
		o.sex = ward.Sex(1 + rng.Intn(2))
		o.iso = rng.Intn(4) == 0
		o.ward = wards[rng.Intn(len(wards))]
	case 2:
		o.kind = "discharge"
		o.pat = "p" + nitoa(rng.Intn(12))
	case 3:
		o.kind = "request"
		o.pat = "p" + nitoa(rng.Intn(12))
		o.to = wards[rng.Intn(len(wards))]
	case 4:
		o.kind = "confirm"
		o.pat = "p" + nitoa(rng.Intn(12))
	case 5:
		o.kind = "cancel"
		o.pat = "p" + nitoa(rng.Intn(12))
	default:
		o.kind = "setiso"
		o.pat = "p" + nitoa(rng.Intn(12))
		o.on = rng.Intn(2) == 0
	}
	return o
}

func fmtLog(o nOp, ik, ires, nk, nres string) string {
	extra := ""
	switch o.kind {
	case "admit":
		sex := "M"
		if o.sex == ward.SexFemale {
			sex = "F"
		}
		extra = fmt.Sprintf("pat=%s sex=%s iso=%v ward=%s", o.pat, sex, o.iso, o.ward)
	case "request":
		extra = fmt.Sprintf("pat=%s to=%s", o.pat, o.to)
	case "setiso":
		extra = fmt.Sprintf("pat=%s on=%v", o.pat, o.on)
	default:
		extra = fmt.Sprintf("pat=%s", o.pat)
	}
	reason := ""
	if ik != "" {
		reason = fmt.Sprintf(" -> REJECT impl=%s naive=%s", ik, nk)
	} else if ires != "" {
		reason = fmt.Sprintf(" -> BED impl=%s naive=%s", ires, nres)
	}
	return fmt.Sprintf("t=%d %-9s op=%-6s %s%s", o.now, o.kind, o.op, extra, reason)
}
