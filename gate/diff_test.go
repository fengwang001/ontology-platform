package gatealloc

import (
	"fmt"
	"math/rand"
	"testing"
)

// diffWorld 是对拍共享的静态描述与操作序列类型。
type diffWorld struct {
	cfg     Config
	gates   []GateSpec
	flights []FlightSpec
	adjs    []Adj
}

type diffOp struct {
	kind   byte // 'a' assign, 'd' delay, 'q' occupant
	now    int
	flight string
	gate   string
	seg    Segment
	mask   DelayMask
	newArr int
	newDep int
	queryT int
}

func buildDiffWorld(rng *rand.Rand) diffWorld {
	cfg := Config{
		Buffer:      rng.Intn(6),
		MaxStay:     40 + rng.Intn(80),
		DeplaneDur:  5 + rng.Intn(20),
		BoardingDur: 5 + rng.Intn(20),
	}
	ng := 3 + rng.Intn(5)
	var gates []GateSpec
	for i := 0; i < ng; i++ {
		gates = append(gates, GateSpec{
			ID:       fmt.Sprintf("G%02d", i),
			MaxClass: Class(1 + rng.Intn(3)),
			Kind:     GateKind(1 + rng.Intn(3)),
		})
	}
	nf := 4 + rng.Intn(8)
	var flights []FlightSpec
	for i := 0; i < nf; i++ {
		arr := 5 + rng.Intn(300)
		dep := arr + 20 + rng.Intn(300)
		flights = append(flights, FlightSpec{
			ID:        fmt.Sprintf("F%02d", i),
			Class:     Class(1 + rng.Intn(3)),
			Kind:      Kind(1 + rng.Intn(2)),
			SchedArr:  arr,
			SchedDep:  dep,
			BoardLead: rng.Intn(40),
		})
	}
	var adjs []Adj
	for i := 0; i < ng; i++ {
		for j := i + 1; j < ng; j++ {
			if rng.Intn(3) == 0 {
				adjs = append(adjs, Adj{A: gates[i].ID, B: gates[j].ID})
			}
		}
	}
	return diffWorld{cfg: cfg, gates: gates, flights: flights, adjs: adjs}
}

func genOps(w diffWorld, rng *rand.Rand, n int) []diffOp {
	var ops []diffOp
	now := 0
	for i := 0; i < n; i++ {
		f := w.flights[rng.Intn(len(w.flights))]
		op := diffOp{flight: f.ID}
		roll := rng.Intn(100)
		switch {
		case roll < 12 && len(w.adjs) > 0:
			// 对抗注入：在一对相邻登机口上安排同区间三级航班，
			// 并构造可能双方受保护的延误，覆盖相邻限制与不可挤占。
			a := w.adjs[rng.Intn(len(w.adjs))]
			if rng.Intn(2) == 0 {
				op.kind = 'a'
				op.now = now
				op.gate = a.A
				op.seg = SegWhole
			} else {
				f2 := w.flights[rng.Intn(len(w.flights))]
				op.flight = f2.ID
				op.kind = 'd'
				op.now = now + rng.Intn(3)
				op.mask = MaskDep
				op.newDep = f2.SchedDep + 60 + rng.Intn(120)
			}
		case roll < 55:
			op.kind = 'a'
			op.now = now + rng.Intn(6)
			op.gate = w.gates[rng.Intn(len(w.gates))].ID
			// 段的选择是否整段由系统状态决定；对拍两边都先尝试 whole，
			// 失败时再试 deplane/boarding。为简单起见随机给段。
			op.seg = Segment(rng.Intn(3))
		case roll < 90:
			op.kind = 'd'
			op.now = now + rng.Intn(6)
			op.mask = DelayMask(1 + rng.Intn(3))
			// 新时刻在计划值基础上推迟一点，偶尔故意提前以触发非法。
			op.newArr = f.SchedArr + rng.Intn(120) - 5
			op.newDep = f.SchedDep + rng.Intn(120) - 5
		default:
			op.kind = 'q'
			op.now = now + rng.Intn(6)
			op.gate = w.gates[rng.Intn(len(w.gates))].ID
			op.queryT = rng.Intn(500)
		}
		now = op.now
		ops = append(ops, op)
	}
	return ops
}

// opOutcome 是一步操作对拍时可比较的输出。
type opOutcome struct {
	ok       bool
	reason   Reason
	conflict string
	split    bool
	evicted  []Eviction
	occID    string
	occSeg   Segment
	occOK    bool
	now      int
}

func runOpOnSystem(s *System, op diffOp) opOutcome {
	switch op.kind {
	case 'a':
		r := s.Assign(op.now, op.flight, op.gate, op.seg)
		o := opOutcome{now: s.Now()}
		if r.OK {
			o.ok = true
		} else {
			o.reason, o.conflict = r.Err.Reason, r.Err.ConflictID
		}
		return o
	case 'd':
		r := s.Delay(op.now, op.flight, op.mask, op.newArr, op.newDep)
		o := opOutcome{now: s.Now(), split: r.BecameSplit}
		if r.OK {
			o.ok, o.evicted = true, r.Evicted
		} else {
			o.reason = r.Err.Reason
		}
		return o
	default:
		id, seg, ok := s.OccupantAt(op.gate, op.queryT)
		return opOutcome{occID: id, occSeg: seg, occOK: ok, now: s.Now()}
	}
}

func runOpOnNaive(n *Naive, op diffOp) opOutcome {
	switch op.kind {
	case 'a':
		r := n.Assign(op.now, op.flight, op.gate, op.seg)
		o := opOutcome{now: n.Now()}
		if r.OK {
			o.ok = true
		} else {
			o.reason, o.conflict = r.Err.Reason, r.Err.ConflictID
		}
		return o
	case 'd':
		r := n.Delay(op.now, op.flight, op.mask, op.newArr, op.newDep)
		o := opOutcome{now: n.Now(), split: r.BecameSplit}
		if r.OK {
			o.ok, o.evicted = true, r.Evicted
		} else {
			o.reason = r.Err.Reason
		}
		return o
	default:
		id, seg, ok := n.OccupantAt(op.gate, op.queryT)
		return opOutcome{occID: id, occSeg: seg, occOK: ok, now: n.Now()}
	}
}

func outcomesEqual(a, b opOutcome) bool {
	if a.ok != b.ok || a.reason != b.reason || a.conflict != b.conflict ||
		a.split != b.split || a.now != b.now ||
		a.occOK != b.occOK || a.occID != b.occID || a.occSeg != b.occSeg {
		return false
	}
	if len(a.evicted) != len(b.evicted) {
		return false
	}
	for i := range a.evicted {
		if a.evicted[i] != b.evicted[i] {
			return false
		}
	}
	return true
}

// TestRandomDifferential：用大量随机操作序列比对正式实现与朴素模型，
// 日志打印每步输入、输出与判定依据（-v 可见）。
func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Logf("differential: inputs/outputs/reason per step follow")
	}
	const worlds, opsPerWorld = 60, 220
	for wi := 0; wi < worlds; wi++ {
		rng := rand.New(rand.NewSource(int64(1000 + wi)))
		w := buildDiffWorld(rng)
		ops := genOps(w, rng, opsPerWorld)

		sys, err := New(w.cfg, w.gates, w.flights, w.adjs)
		if err != nil {
			t.Fatalf("world %d New: %v", wi, err)
		}
		nai, err := NewNaive(w.cfg, w.gates, w.flights, w.adjs)
		if err != nil {
			t.Fatalf("world %d NewNaive: %v", wi, err)
		}

		for step, op := range ops {
			so := runOpOnSystem(sys, op)
			no := runOpOnNaive(nai, op)
			if testing.Verbose() && (wi < 2 || !outcomesEqual(so, no)) {
				t.Logf("w=%d step=%d op=%+v sys={ok:%v reason:%s conf:%s split:%v evict:%v now:%d occ:%s/%v} naive={ok:%v reason:%s conf:%s split:%v evict:%v now:%d occ:%s/%v}",
					wi, step, op,
					so.ok, so.reason, so.conflict, so.split, so.evicted, so.now, so.occID, so.occOK,
					no.ok, no.reason, no.conflict, no.split, no.evicted, no.now, no.occID, no.occOK)
			}
			if !outcomesEqual(so, no) {
				t.Fatalf("world %d step %d outcome mismatch:\nop=%+v\nsys=%+v\nnaive=%+v", wi, step, op, so, no)
			}
			if !snapshotsEqual(sys.Snapshot(), nai.Snapshot()) {
				t.Fatalf("world %d step %d snapshot mismatch:\nop=%+v\n%+v\n%+v", wi, step, op, sys.Snapshot(), nai.Snapshot())
			}
		}
	}
}

// TestDeterministicReplay：同一序列重放两次，结果逐字节一致。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	w := buildDiffWorld(rng)
	ops := genOps(w, rng, 300)
	run := func() (Snapshot, []opOutcome) {
		s, _ := New(w.cfg, w.gates, w.flights, w.adjs)
		var outs []opOutcome
		for _, op := range ops {
			outs = append(outs, runOpOnSystem(s, op))
		}
		return s.Snapshot(), outs
	}
	s1, o1 := run()
	s2, o2 := run()
	if !snapshotsEqual(s1, s2) {
		t.Fatal("replay snapshot differs")
	}
	for i := range o1 {
		if !outcomesEqual(o1[i], o2[i]) {
			t.Fatalf("replay outcome differs at %d", i)
		}
	}
}

// TestConcurrentSerializable：并发调用后系统不变量成立（与某串行序等价）。
func TestConcurrentSerializable(t *testing.T) {
	cfg := Config{Buffer: 2, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "A", Class: Class1, Kind: KindDomestic, SchedArr: 100, SchedDep: 200, BoardLead: 10},
		{ID: "B", Class: Class1, Kind: KindDomestic, SchedArr: 100, SchedDep: 200, BoardLead: 10},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, nil)
	done := make(chan AssignResult, 2)
	go func() { done <- s.Assign(0, "A", "G1", SegWhole) }()
	go func() { done <- s.Assign(0, "B", "G1", SegWhole) }()
	r1, r2 := <-done, <-done
	if r1.OK == r2.OK {
		t.Fatal("exactly one of two concurrent overlapping assigns must win")
	}
	assertNoOverlaps(t, s)
}

func assertNoOverlaps(t *testing.T, s *System) {
	snap := s.Snapshot()
	byGate := map[string][]Assignment{}
	for _, a := range snap.Assignments {
		byGate[a.Gate] = append(byGate[a.Gate], a)
	}
	for g, list := range byGate {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if overlap(list[i].Start, list[i].End, list[j].Start, list[j].End) {
					t.Fatalf("invariant broken on %s: %+v vs %+v", g, list[i], list[j])
				}
			}
		}
	}
}

// TestAdversarialDifferential：专门覆盖相邻限制、四级挤占与不可挤占的对拍。
func TestAdversarialDifferential(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		rng := rand.New(rand.NewSource(int64(7000 + iter)))
		cfg := Config{Buffer: rng.Intn(4), MaxStay: 100000, DeplaneDur: 10, BoardingDur: 10}
		gates := []GateSpec{
			{ID: "GA", MaxClass: Class3, Kind: GateDual},
			{ID: "GB", MaxClass: Class3, Kind: GateDual},
			{ID: "GC", MaxClass: Class3, Kind: GateDual},
		}
		// 全部三级；属性在国际/国内间随机；计划时刻交错。
		var flights []FlightSpec
		nf := 3 + rng.Intn(4)
		for i := 0; i < nf; i++ {
			arr := 10 + rng.Intn(200)
			flights = append(flights, FlightSpec{
				ID:        fmt.Sprintf("P%02d", i),
				Class:     Class3,
				Kind:      Kind(1 + rng.Intn(2)),
				SchedArr:  arr,
				SchedDep:  arr + 30 + rng.Intn(120),
				BoardLead: rng.Intn(50),
			})
		}
		w := diffWorld{
			cfg: cfg, gates: gates, flights: flights,
			adjs: []Adj{{A: "GA", B: "GB"}, {A: "GB", B: "GC"}},
		}
		var ops []diffOp
		now := 0
		for k := 0; k < 120; k++ {
			f := flights[rng.Intn(len(flights))]
			op := diffOp{flight: f.ID, now: now}
			switch rng.Intn(3) {
			case 0:
				op.kind = 'a'
				op.gate = gates[rng.Intn(3)].ID
				op.seg = SegWhole
			case 1:
				op.kind = 'd'
				op.mask = DelayMask(1 + rng.Intn(3))
				op.newArr = f.SchedArr + rng.Intn(160)
				op.newDep = f.SchedDep + rng.Intn(160)
				if rng.Intn(6) == 0 {
					op.newArr = f.SchedArr - 1 // 偶发提前 -> invalid_param
				}
			default:
				op.kind = 'q'
				op.gate = gates[rng.Intn(3)].ID
				op.queryT = rng.Intn(400)
			}
			now += rng.Intn(4)
			ops = append(ops, op)
		}
		sys, _ := New(w.cfg, w.gates, w.flights, w.adjs)
		nai, _ := NewNaive(w.cfg, w.gates, w.flights, w.adjs)
		for step, op := range ops {
			so, no := runOpOnSystem(sys, op), runOpOnNaive(nai, op)
			if !outcomesEqual(so, no) {
				t.Fatalf("iter %d step %d mismatch op=%+v sys=%+v naive=%+v", iter, step, op, so, no)
			}
			ssp, nsp := sys.Snapshot(), nai.Snapshot()
			if !snapshotsEqual(ssp, nsp) {
				t.Fatalf("iter %d step %d snapshot mismatch op=%+v\nSYS=%+v\nNAIVE=%+v", iter, step, op, ssp, nsp)
			}
		}
	}
}
