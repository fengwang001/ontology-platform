package gate

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素对照模型：按题面规则独立实现，线性扫描、无索引结构，
// 与 System 共享类型定义但不共享任何判定逻辑。

type nSeg struct {
	start, end int
	gate       string
}

type nFlight struct {
	f    Flight
	segs map[SegKind]*nSeg
}

type nGate struct {
	maxLevel Level
	kind     GateKind
	adj      map[string]bool
}

type naiveSystem struct {
	cfg     Config
	now     int
	gates   map[string]*nGate
	flights map[string]*nFlight
}

func newNaiveSystem(cfg Config) *naiveSystem {
	return &naiveSystem{cfg: cfg, gates: map[string]*nGate{}, flights: map[string]*nFlight{}}
}

func nSplit(arr, dep int, cfg Config) bool { return dep+cfg.Buffer-arr > cfg.StayLimit }

func nCompat(fk FlightKind, gk GateKind) bool {
	return gk == GateDual || (fk == FlightInternational) == (gk == GateInternational)
}

func nHigher(a, b *Flight) bool {
	if a.Kind != b.Kind {
		return a.Kind == FlightInternational
	}
	if a.Level != b.Level {
		return a.Level > b.Level
	}
	if a.SchedArr != b.SchedArr {
		return a.SchedArr < b.SchedArr
	}
	return a.ID < b.ID
}

func nProtected(arr, dep, lead, now int) bool {
	return now >= arr || now >= dep-lead
}

func nOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart < bEnd && aEnd > bStart
}

func (n *naiveSystem) AddGate(spec GateSpec, now int) Result {
	if now < 0 || spec.ID == "" || spec.MaxLevel < 1 || spec.MaxLevel > MaxLevel ||
		spec.Kind < GateInternational || spec.Kind > GateDual {
		return reject(ReasonInvalidParam, "bad gate spec %+v", spec)
	}
	if now < n.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, n.now)
	}
	if _, dup := n.gates[spec.ID]; dup {
		return reject(ReasonInvalidParam, "gate %s already exists", spec.ID)
	}
	adj := map[string]bool{}
	for _, a := range spec.Adjacent {
		if a == spec.ID {
			return reject(ReasonInvalidParam, "gate %s adjacent to itself", spec.ID)
		}
		if _, ok := n.gates[a]; !ok {
			return reject(ReasonNotFound, "adjacent gate %s not found", a)
		}
		adj[a] = true
	}
	n.gates[spec.ID] = &nGate{maxLevel: spec.MaxLevel, kind: spec.Kind, adj: adj}
	for a := range adj {
		n.gates[a].adj[spec.ID] = true
	}
	n.now = now
	return okResult("gate %s added", spec.ID)
}

func (n *naiveSystem) buildSegs(f *Flight) map[SegKind]*nSeg {
	out := map[SegKind]*nSeg{}
	if nSplit(f.CurArr, f.CurDep, n.cfg) {
		out[SegDeplane] = &nSeg{start: f.CurArr, end: f.CurArr + n.cfg.Deplane}
		out[SegBoard] = &nSeg{start: f.CurDep - n.cfg.Board, end: f.CurDep + n.cfg.Buffer}
	} else {
		out[SegWhole] = &nSeg{start: f.CurArr, end: f.CurDep + n.cfg.Buffer}
	}
	return out
}

func (n *naiveSystem) AddFlight(spec FlightSpec, now int) Result {
	if now < 0 || spec.ID == "" || spec.Level < 1 || spec.Level > MaxLevel ||
		spec.Kind < FlightInternational || spec.Kind > FlightDomestic ||
		spec.SchedArr < 0 || spec.SchedDep < spec.SchedArr || spec.BoardLead < 0 {
		return reject(ReasonInvalidParam, "bad flight spec %+v", spec)
	}
	if now < n.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, n.now)
	}
	if _, dup := n.flights[spec.ID]; dup {
		return reject(ReasonInvalidParam, "flight %s already exists", spec.ID)
	}
	nf := &nFlight{f: Flight{
		ID: spec.ID, Level: spec.Level, Kind: spec.Kind,
		SchedArr: spec.SchedArr, SchedDep: spec.SchedDep, BoardLead: spec.BoardLead,
		CurArr: spec.SchedArr, CurDep: spec.SchedDep,
	}}
	nf.segs = n.buildSegs(&nf.f)
	n.flights[spec.ID] = nf
	n.now = now
	return okResult("flight %s added", spec.ID)
}

func (n *naiveSystem) Assign(fid string, sk SegKind, gid string, now int) Result {
	if now < 0 || sk < SegWhole || sk > SegBoard {
		return reject(ReasonInvalidParam, "bad params")
	}
	if now < n.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, n.now)
	}
	fl, ok := n.flights[fid]
	if !ok {
		return reject(ReasonNotFound, "flight %s not found", fid)
	}
	g, ok := n.gates[gid]
	if !ok {
		return reject(ReasonNotFound, "gate %s not found", gid)
	}
	sg, ok := fl.segs[sk]
	if !ok {
		return reject(ReasonInvalidParam, "flight %s has no %s segment", fid, sk)
	}
	if now >= fl.f.CurArr {
		return reject(ReasonImmutable, "flight %s already arrived", fid)
	}
	if fl.f.Level > g.maxLevel {
		return reject(ReasonLevelIncompatible, "level %d > gate max %d", fl.f.Level, g.maxLevel)
	}
	if !nCompat(fl.f.Kind, g.kind) {
		return reject(ReasonKindMismatch, "kind mismatch")
	}
	if fl.f.Level == MaxLevel {
		for _, of := range n.flights {
			if of == fl || of.f.Level != MaxLevel {
				continue
			}
			for _, os := range of.segs {
				if os.gate == "" || !g.adj[os.gate] || os.end <= now {
					continue
				}
				if nOverlap(sg.start, sg.end, os.start, os.end) {
					return reject(ReasonAdjacency, "adjacent gate %s has max-level flight %s",
						os.gate, of.f.ID)
				}
			}
		}
	}
	var bestID string
	bestStart := 0
	found := false
	for _, of := range n.flights {
		if of == fl {
			continue
		}
		for _, os := range of.segs {
			if os.gate != gid || os.end <= now {
				continue
			}
			if nOverlap(sg.start, sg.end, os.start, os.end) {
				if !found || os.start < bestStart || (os.start == bestStart && of.f.ID < bestID) {
					bestID, bestStart, found = of.f.ID, os.start, true
				}
			}
		}
	}
	if found {
		r := reject(ReasonTimeConflict, "overlaps flight %s", bestID)
		r.Conflict = bestID
		return r
	}
	sg.gate = gid
	n.now = now
	return okResult("flight %s %s assigned to gate %s", fid, sk, gid)
}

func (n *naiveSystem) DelayArrival(fid string, t, now int) Result {
	return n.delay(fid, true, t, now)
}

func (n *naiveSystem) DelayDeparture(fid string, t, now int) Result {
	return n.delay(fid, false, t, now)
}

func (n *naiveSystem) delay(fid string, isArr bool, t, now int) Result {
	if now < 0 || t < 0 {
		return reject(ReasonInvalidParam, "negative time")
	}
	if now < n.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, n.now)
	}
	fl, ok := n.flights[fid]
	if !ok {
		return reject(ReasonNotFound, "flight %s not found", fid)
	}
	cur := fl.f.CurDep
	if isArr {
		cur = fl.f.CurArr
	}
	if t < cur {
		return reject(ReasonInvalidParam, "only postponement allowed")
	}
	newArr, newDep := fl.f.CurArr, fl.f.CurDep
	if isArr {
		newArr = t
	} else {
		newDep = t
	}
	if newArr > newDep {
		return reject(ReasonInvalidParam, "arrival after departure")
	}
	if isArr && now >= fl.f.CurArr {
		return reject(ReasonImmutable, "flight %s already arrived", fid)
	}

	oldSegs := fl.segs
	newSegs := map[SegKind]*nSeg{}
	if nSplit(newArr, newDep, n.cfg) {
		dg := ""
		if old, ok := oldSegs[SegDeplane]; ok {
			dg = old.gate
		} else if old, ok := oldSegs[SegWhole]; ok {
			dg = old.gate
		}
		bg := ""
		if old, ok := oldSegs[SegBoard]; ok {
			bg = old.gate
		}
		newSegs[SegDeplane] = &nSeg{start: newArr, end: newArr + n.cfg.Deplane, gate: dg}
		newSegs[SegBoard] = &nSeg{start: newDep - n.cfg.Board, end: newDep + n.cfg.Buffer, gate: bg}
	} else {
		g := ""
		if old, ok := oldSegs[SegWhole]; ok {
			g = old.gate
		} else if old, ok := oldSegs[SegDeplane]; ok {
			g = old.gate
		}
		newSegs[SegWhole] = &nSeg{start: newArr, end: newDep + n.cfg.Buffer, gate: g}
	}

	type nConflict struct {
		my       SegKind
		other    *nFlight
		otherSeg *nSeg
		oKind    SegKind
	}
	var conflicts []nConflict
	for _, k := range []SegKind{SegWhole, SegDeplane, SegBoard} {
		ns, ok := newSegs[k]
		if !ok || ns.gate == "" {
			continue
		}
		for _, of := range n.flights {
			if of == fl {
				continue
			}
			for oKind, os := range of.segs {
				if os.gate == "" || os.end <= now {
					continue
				}
				if !nOverlap(ns.start, ns.end, os.start, os.end) {
					continue
				}
				if os.gate == ns.gate {
					conflicts = append(conflicts, nConflict{my: k, other: of, otherSeg: os, oKind: oKind})
				} else if fl.f.Level == MaxLevel && of.f.Level == MaxLevel &&
					n.gates[ns.gate].adj[os.gate] {
					conflicts = append(conflicts, nConflict{my: k, other: of, otherSeg: os, oKind: oKind})
				}
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		a, b := conflicts[i], conflicts[j]
		if a.otherSeg.start != b.otherSeg.start {
			return a.otherSeg.start < b.otherSeg.start
		}
		if a.other.f.ID != b.other.f.ID {
			return a.other.f.ID < b.other.f.ID
		}
		if a.oKind != b.oKind {
			return a.oKind < b.oKind
		}
		return a.my < b.my
	})

	meP := nProtected(newArr, newDep, fl.f.BoardLead, now)
	myLost := map[SegKind]bool{}
	bumpedSegs := map[*nSeg]bool{}
	bumpedSet := map[string]bool{}
	for _, c := range conflicts {
		if myLost[c.my] || bumpedSegs[c.otherSeg] {
			continue
		}
		oP := nProtected(c.other.f.CurArr, c.other.f.CurDep, c.other.f.BoardLead, now)
		switch {
		case meP && oP:
			return reject(ReasonNotBumpable, "both %s and %s unbumpable", fid, c.other.f.ID)
		case oP:
			myLost[c.my] = true
		case meP:
			bumpedSegs[c.otherSeg] = true
			bumpedSet[c.other.f.ID] = true
		default:
			if nHigher(&fl.f, &c.other.f) {
				bumpedSegs[c.otherSeg] = true
				bumpedSet[c.other.f.ID] = true
			} else {
				myLost[c.my] = true
			}
		}
	}

	for os := range bumpedSegs {
		os.gate = ""
	}
	lostSelf := false
	for k, ns := range newSegs {
		if myLost[k] {
			ns.gate = ""
			lostSelf = true
		}
	}
	fl.f.CurArr, fl.f.CurDep = newArr, newDep
	fl.segs = newSegs
	var bumped []string
	for id := range bumpedSet {
		bumped = append(bumped, id)
	}
	if lostSelf {
		bumped = append(bumped, fid)
	}
	sort.Strings(bumped)
	n.now = now
	return Result{OK: true, Reason: ReasonOK, Bumped: bumped,
		Detail: fmt.Sprintf("flight %s delayed to [%d,%d], bumped=%v", fid, newArr, newDep, bumped)}
}

func (n *naiveSystem) Occupant(gid string, t int) (string, SegKind, bool) {
	if _, ok := n.gates[gid]; !ok {
		return "", SegWhole, false
	}
	ids := make([]string, 0, len(n.flights))
	for id := range n.flights {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fl := n.flights[id]
		for _, k := range []SegKind{SegWhole, SegDeplane, SegBoard} {
			if sg, ok := fl.segs[k]; ok && sg.gate == gid && sg.end > n.now &&
				sg.start <= t && t < sg.end {
				return id, k, true
			}
		}
	}
	return "", SegWhole, false
}

func (n *naiveSystem) Snapshot() map[string]SnapFlight {
	out := map[string]SnapFlight{}
	for id, fl := range n.flights {
		sf := SnapFlight{CurArr: fl.f.CurArr, CurDep: fl.f.CurDep, Gates: map[SegKind]string{}}
		for k, sg := range fl.segs {
			sf.Gates[k] = sg.gate
		}
		out[id] = sf
	}
	return out
}

// 随机操作序列下 System 与朴素对照模型逐步比对，
// 日志打印每步的输入、输出与判定依据。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	cfg := Config{Buffer: 15, StayLimit: 180, Deplane: 30, Board: 45}
	for seed := int64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			sys, err := NewSystem(cfg)
			if err != nil {
				t.Fatal(err)
			}
			nav := newNaiveSystem(cfg)
			now := 0
			var gateIDs, flightIDs []string

			check := func(step int, op string, r1, r2 Result) {
				t.Helper()
				t.Logf("step %d now=%d %s\n  sys: ok=%v reason=%s conflict=%q bumped=%v | %s\n  nav: ok=%v reason=%s conflict=%q bumped=%v | %s",
					step, now, op,
					r1.OK, r1.Reason, r1.Conflict, r1.Bumped, r1.Detail,
					r2.OK, r2.Reason, r2.Conflict, r2.Bumped, r2.Detail)
				if r1.OK != r2.OK || r1.Reason != r2.Reason || r1.Conflict != r2.Conflict ||
					!reflect.DeepEqual(r1.Bumped, r2.Bumped) {
					t.Fatalf("step %d %s: result mismatch\n sys=%+v\n nav=%+v", step, op, r1, r2)
				}
				s1, s2 := sys.Snapshot(), nav.Snapshot()
				if !reflect.DeepEqual(s1, s2) {
					t.Fatalf("step %d %s: snapshot mismatch\n sys=%v\n nav=%v", step, op, s1, s2)
				}
				if err := sys.CheckConsistency(); err != nil {
					t.Fatalf("step %d %s: consistency violated: %v", step, op, err)
				}
			}

			for i := 0; i < 4; i++ {
				id := fmt.Sprintf("G%d", i)
				var adj []string
				for _, g := range gateIDs {
					if r.Intn(2) == 0 {
						adj = append(adj, g)
					}
				}
				spec := GateSpec{ID: id, MaxLevel: Level(1 + r.Intn(3)), Kind: GateKind(r.Intn(3)), Adjacent: adj}
				check(i, "AddGate "+id, sys.AddGate(spec, now), nav.AddGate(spec, now))
				gateIDs = append(gateIDs, id)
			}
			for i := 0; i < 8; i++ {
				id := fmt.Sprintf("F%d", i)
				arr := r.Intn(400)
				spec := FlightSpec{
					ID: id, Level: Level(1 + r.Intn(3)), Kind: FlightKind(r.Intn(2)),
					SchedArr: arr, SchedDep: arr + 30 + r.Intn(400), BoardLead: r.Intn(90),
				}
				check(10+i, "AddFlight "+id, sys.AddFlight(spec, now), nav.AddFlight(spec, now))
				flightIDs = append(flightIDs, id)
			}

			for step := 0; step < 150; step++ {
				if r.Intn(10) > 0 {
					now += r.Intn(40)
				} else {
					now -= r.Intn(20) // 偶发时钟回退/负值
				}
				snap := sys.Snapshot()
				pick := r.Intn(100)
				switch {
				case pick < 8 && len(gateIDs) < 8: // 新登机口
					id := fmt.Sprintf("GX%d", len(gateIDs))
					var adj []string
					for _, g := range gateIDs {
						if r.Intn(2) == 0 {
							adj = append(adj, g)
						}
					}
					spec := GateSpec{ID: id, MaxLevel: Level(1 + r.Intn(3)), Kind: GateKind(r.Intn(3)), Adjacent: adj}
					check(step, "AddGate "+id, sys.AddGate(spec, now), nav.AddGate(spec, now))
					gateIDs = append(gateIDs, id)
				case pick < 20: // 新航班
					id := fmt.Sprintf("FN%d", len(flightIDs))
					arr := now + r.Intn(300)
					if r.Intn(10) == 0 {
						arr = r.Intn(100) // 偶发过去时刻
					}
					spec := FlightSpec{
						ID: id, Level: Level(1 + r.Intn(3)), Kind: FlightKind(r.Intn(2)),
						SchedArr: arr, SchedDep: arr + 30 + r.Intn(400), BoardLead: r.Intn(90),
					}
					check(step, "AddFlight "+id, sys.AddFlight(spec, now), nav.AddFlight(spec, now))
					flightIDs = append(flightIDs, id)
				case pick < 55: // 指派/改派
					fid := flightIDs[r.Intn(len(flightIDs))]
					seg := SegKind(r.Intn(3))
					if r.Intn(10) < 7 {
						var keys []SegKind
						if sf, ok := snap[fid]; ok {
							for k := range sf.Gates {
								keys = append(keys, k)
							}
						}
						if len(keys) > 0 {
							sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
							seg = keys[r.Intn(len(keys))]
						}
					}
					gid := gateIDs[r.Intn(len(gateIDs))]
					if r.Intn(2) == 0 {
						gid = gateIDs[0] // 热点登机口，提高冲突概率
					}
					op := fmt.Sprintf("Assign %s %s %s", fid, seg, gid)
					check(step, op, sys.Assign(fid, seg, gid, now), nav.Assign(fid, seg, gid, now))
				case pick < 75: // 延误起飞
					fid := flightIDs[r.Intn(len(flightIDs))]
					delta := r.Intn(400)
					if r.Intn(8) == 0 {
						delta = -r.Intn(60) // 偶发提前（非法）
					}
					nd := snap[fid].CurDep + delta
					op := fmt.Sprintf("DelayDeparture %s %d", fid, nd)
					check(step, op, sys.DelayDeparture(fid, nd, now), nav.DelayDeparture(fid, nd, now))
				case pick < 95: // 延误到达
					fid := flightIDs[r.Intn(len(flightIDs))]
					delta := r.Intn(150)
					if r.Intn(8) == 0 {
						delta = -r.Intn(60)
					}
					na := snap[fid].CurArr + delta
					op := fmt.Sprintf("DelayArrival %s %d", fid, na)
					check(step, op, sys.DelayArrival(fid, na, now), nav.DelayArrival(fid, na, now))
				default: // 占用查询
					gid := gateIDs[r.Intn(len(gateIDs))]
					ts := now + r.Intn(200) - 50
					id1, k1, ok1 := sys.Occupant(gid, ts)
					id2, k2, ok2 := nav.Occupant(gid, ts)
					t.Logf("step %d now=%d Occupant %s %d -> sys=(%q,%v,%v) nav=(%q,%v,%v)",
						step, now, gid, ts, id1, k1, ok1, id2, k2, ok2)
					if id1 != id2 || k1 != k2 || ok1 != ok2 {
						t.Fatalf("step %d occupant mismatch", step)
					}
				}
			}
		})
	}
}

// 挤占密集场景：单热点登机口 + 密集航班 + 大幅延误，确保覆盖挤占裁决路径。
func TestRandomizedBumpHeavy(t *testing.T) {
	cfg := Config{Buffer: 15, StayLimit: 600, Deplane: 30, Board: 45}
	totalBumps := 0
	for seed := int64(100); seed < 108; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			sys, err := NewSystem(cfg)
			if err != nil {
				t.Fatal(err)
			}
			nav := newNaiveSystem(cfg)
			now := 0
			check := func(step int, op string, r1, r2 Result) {
				t.Helper()
				t.Logf("step %d now=%d %s\n  sys: ok=%v reason=%s conflict=%q bumped=%v | %s\n  nav: ok=%v reason=%s conflict=%q bumped=%v | %s",
					step, now, op,
					r1.OK, r1.Reason, r1.Conflict, r1.Bumped, r1.Detail,
					r2.OK, r2.Reason, r2.Conflict, r2.Bumped, r2.Detail)
				if r1.OK != r2.OK || r1.Reason != r2.Reason || r1.Conflict != r2.Conflict ||
					!reflect.DeepEqual(r1.Bumped, r2.Bumped) {
					t.Fatalf("step %d %s: result mismatch\n sys=%+v\n nav=%+v", step, op, r1, r2)
				}
				if !reflect.DeepEqual(sys.Snapshot(), nav.Snapshot()) {
					t.Fatalf("step %d %s: snapshot mismatch", step, op)
				}
				if err := sys.CheckConsistency(); err != nil {
					t.Fatalf("step %d %s: consistency violated: %v", step, op, err)
				}
				totalBumps += len(r1.Bumped)
			}
			check(0, "AddGate HOT", sys.AddGate(GateSpec{ID: "HOT", MaxLevel: 3, Kind: GateDual}, 0),
				nav.AddGate(GateSpec{ID: "HOT", MaxLevel: 3, Kind: GateDual}, 0))
			check(1, "AddGate AUX", sys.AddGate(GateSpec{ID: "AUX", MaxLevel: 3, Kind: GateDual}, 0),
				nav.AddGate(GateSpec{ID: "AUX", MaxLevel: 3, Kind: GateDual}, 0))
			var flightIDs []string
			for i := 0; i < 14; i++ {
				id := fmt.Sprintf("F%d", i)
				arr := 50 + r.Intn(300)
				spec := FlightSpec{
					ID: id, Level: Level(1 + r.Intn(3)), Kind: FlightKind(r.Intn(2)),
					SchedArr: arr, SchedDep: arr + 60 + r.Intn(150), BoardLead: 30 + r.Intn(60),
				}
				check(2+i, "AddFlight "+id, sys.AddFlight(spec, now), nav.AddFlight(spec, now))
				flightIDs = append(flightIDs, id)
			}
			for step := 0; step < 120; step++ {
				now += r.Intn(8)
				fid := flightIDs[r.Intn(len(flightIDs))]
				snap := sys.Snapshot()
				if r.Intn(2) == 0 {
					seg := SegWhole
					if _, ok := snap[fid].Gates[SegDeplane]; ok {
						seg = SegDeplane
					}
					gid := "HOT"
					if r.Intn(4) == 0 {
						gid = "AUX"
					}
					op := fmt.Sprintf("Assign %s %s %s", fid, seg, gid)
					check(step, op, sys.Assign(fid, seg, gid, now), nav.Assign(fid, seg, gid, now))
				} else {
					nd := snap[fid].CurDep + r.Intn(500)
					op := fmt.Sprintf("DelayDeparture %s %d", fid, nd)
					check(step, op, sys.DelayDeparture(fid, nd, now), nav.DelayDeparture(fid, nd, now))
				}
			}
		})
	}
	t.Logf("total bumps across seeds: %d", totalBumps)
	if totalBumps == 0 {
		t.Fatal("bump-heavy scenario never exercised adjudication")
	}
}
