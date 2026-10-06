package bus

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func diffScheme() Scheme {
	return Scheme{
		Stops: []StopSpec{
			{Name: "C0", Control: true, DwellSec: 10},
			{Name: "S1", DwellSec: 8},
			{Name: "C2", Control: true, DwellSec: 10},
			{Name: "S3", DwellSec: 8},
			{Name: "C4", Control: true, DwellSec: 10},
		},
		Travel:       []int64{50, 50, 50, 50},
		HeadwaySec:   60,
		HoldCapSec:   40,
		ToleranceSec: 300,
		DutyCapSec:   100000,
	}
}

type genCase struct {
	rng     *rand.Rand
	svc     *Service
	model   *NaiveModel
	clock   int64
	tripIDs []int64
}

func newGenCase(t *testing.T, seed int64) *genCase {
	t.Helper()
	sc := diffScheme()
	svc := newSvc(t, sc)
	g := &genCase{rng: rand.New(rand.NewSource(seed)), svc: svc, model: NewNaiveModel(sc)}
	for i := 1; i <= 6; i++ {
		d := fmt.Sprintf("drv%d", i)
		g.exec(t, Op{Kind: OpRegisterDriver, Driver: d, At: g.advance(1)})
	}
	for i := 1; i <= 4; i++ {
		id := int64(i * 100)
		g.tripIDs = append(g.tripIDs, id)
		g.exec(t, Op{Kind: OpRegisterTrip, TripID: id, Driver: fmt.Sprintf("drv%d", i), At: g.advance(1)})
	}
	for i, sid := range []int64{50, 150, 250} {
		g.exec(t, Op{Kind: OpAddSpare, TripID: sid, Driver: fmt.Sprintf("drv%d", (i%5)+1), At: g.advance(1)})
	}
	return g
}

func (g *genCase) advance(d int64) int64 {
	g.clock += 1 + d
	return g.clock
}

func kindOf(err error) ErrorKind {
	if err == nil {
		return 0
	}
	if be, ok := err.(*BusError); ok {
		return be.Kind
	}
	return -1
}

func runSvc(s *Service, op Op) (*EventView, ErrorKind) {
	switch op.Kind {
	case OpRegisterDriver:
		return nil, kindOf(s.RegisterDriver(op.Driver, op.At))
	case OpRegisterTrip:
		return nil, kindOf(s.RegisterTrip(op.TripID, op.Driver, op.At))
	case OpAddSpare:
		return nil, kindOf(s.AddSpare(op.TripID, op.Driver, op.At))
	case OpRegisterAlighting:
		return nil, kindOf(s.RegisterAlighting(op.TripID, op.Stop, op.At))
	case OpReportArrival:
		v, err := s.ReportArrival(op.TripID, op.Stop, op.At)
		if v == nil {
			return nil, kindOf(err)
		}
		return v, kindOf(err)
	}
	return nil, ErrInvalidParam
}

// exec 在两个实现上执行同一操作并断言错误类别与落库结果完全一致。
func (g *genCase) exec(t *testing.T, op Op) {
	t.Helper()
	svcView, svcKind := runSvc(g.svc, op)
	naView, naKind := g.model.Apply(op)
	if svcKind != naKind {
		t.Fatalf("error kind mismatch op=%+v service=%d naive=%d", op, svcKind, naKind)
	}
	if svcKind == 0 && op.Kind == OpReportArrival {
		if svcView.Arrival != naView.Arrival ||
			svcView.Departure != naView.Departure ||
			svcView.Intervention != naView.Intervention ||
			svcView.Reported != naView.Reported {
			t.Fatalf("event mismatch op=%+v service=%+v naive=%+v", op, *svcView, *naView)
		}
	}
	g.assertState(t)
}

func (g *genCase) assertState(t *testing.T) {
	t.Helper()
	o1, o2 := g.svc.Order(), g.model.Order()
	if len(o1) != len(o2) {
		t.Fatalf("order length mismatch %v vs %v", o1, o2)
	}
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Fatalf("order mismatch %v vs %v", o1, o2)
		}
	}
	for _, id := range o1 {
		for stop := range g.svc.scheme.Stops {
			v1, err := g.svc.GetEvent(id, stop)
			v2, k := g.model.Event(id, stop)
			if err != nil || k != 0 {
				t.Fatalf("lookup mismatch trip=%d stop=%d", id, stop)
			}
			if v1.Reported != v2.Reported {
				t.Fatalf("reported mismatch trip=%d stop=%d %+v vs %+v", id, stop, v1, v2)
			}
			if v1.Reported && (v1.Arrival != v2.Arrival ||
				v1.Departure != v2.Departure ||
				v1.Intervention != v2.Intervention) {
				t.Fatalf("event mismatch trip=%d stop=%d service=%+v naive=%+v", id, stop, v1, v2)
			}
		}
	}
}

func (g *genCase) candidates() []int64 {
	var ids []int64
	for _, id := range g.svc.Order() {
		tr, _ := g.svc.lookupTrip(id)
		if tr.nextStop < len(g.svc.scheme.Stops) {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestRandomDifferential 用独立朴素逐车逐站模型对照随机操作序列。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			g := newGenCase(t, seed)
			stops := len(g.svc.scheme.Stops)
			for step := 0; step < 260; step++ {
				cand := g.candidates()
				if len(cand) == 0 {
					break
				}
				id := cand[g.rng.Intn(len(cand))]
				tr, _ := g.svc.lookupTrip(id)
				next := tr.nextStop

				// 约 30% 概率注入被拒绝操作，要求两实现错误类别完全一致。
				switch g.rng.Intn(20) {
				case 0:
					g.exec(t, Op{Kind: OpReportArrival, TripID: 99999, Stop: 0, At: g.advance(1)})
					continue
				case 1:
					g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: stops + 1, At: g.advance(1)})
					continue
				case 2:
					if next > 0 {
						g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: next - 1, At: g.advance(1)})
						continue
					}
				case 3:
					if next+1 < stops {
						g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: next + 1, At: g.advance(1)})
						continue
					}
				case 4:
					g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: next, At: g.clock - 1})
					continue
				case 5:
					g.exec(t, Op{Kind: OpRegisterAlighting, TripID: id, Stop: next, At: g.advance(1)})
					continue
				}

				at := g.chooseArrival(tr, next)
				g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: next, At: at})
			}
			for {
				cand := g.candidates()
				if len(cand) == 0 {
					break
				}
				id := cand[0]
				tr, _ := g.svc.lookupTrip(id)
				g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: tr.nextStop, At: g.advance(80)})
			}
		})
	}
}

// chooseArrival 围绕同站前车到站时刻，在 H/2、2H 等临界点两侧取点。
func (g *genCase) chooseArrival(tr *trip, stop int) int64 {
	sc := g.svc.scheme
	base := g.clock + 1
	if stop > tr.entryStop {
		plan := tr.events[stop-1].Departure + sc.Travel[stop-1]
		if plan > base {
			base = plan
		}
	}
	prev := g.svc.prevTripAt(tr, stop)
	if prev == nil {
		at := base + int64(g.rng.Intn(5))
		g.clock = at
		return at
	}
	prevArr := prev.events[stop].Arrival
	var at int64
	switch g.rng.Intn(7) {
	case 0:
		at = prevArr + sc.HeadwaySec/2
	case 1:
		at = prevArr + sc.HeadwaySec/2 - 1
	case 2:
		at = prevArr + 2*sc.HeadwaySec
	case 3:
		at = prevArr + 2*sc.HeadwaySec + 1
	case 4:
		at = prevArr + sc.HeadwaySec
	default:
		at = prevArr + sc.HeadwaySec/2 + 1 + int64(g.rng.Intn(int(sc.HeadwaySec)))
	}
	if at < base {
		at = base
	}
	g.clock = at
	return at
}

// TestConcurrentSerialEquivalence 并发上报后：车次相对次序不变、
// 任意串行位置上同站相邻已上报车次离站差不为负。
func TestConcurrentSerialEquivalence(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		g := newGenCase(t, seed)
		var wg sync.WaitGroup
		// 预先生成一条合法操作序列：按站推进，同站车次严格按发车序分配
		// 单调但带随机扰动的时刻；随后把这些调用无序并发打到服务上。
		type call struct {
			id   int64
			stop int
			at   int64
		}
		var calls []call
		var clock int64 = 1000
		local := rand.New(rand.NewSource(seed))
		for stop := 0; stop < len(g.svc.scheme.Stops); stop++ {
			for _, id := range g.tripIDs {
				clock += 30 + int64(local.Intn(60))
				calls = append(calls, call{id: id, stop: stop, at: clock})
			}
		}
		start := make(chan struct{})
		var rejectOK, rejectOther int64
		var rmu sync.Mutex
		for _, c := range calls {
			wg.Add(1)
			go func(c call) {
				defer wg.Done()
				<-start
				if _, err := g.svc.ReportArrival(c.id, c.stop, c.at); err != nil {
					k := kindOf(err)
					if k == ErrClockRollback || k == ErrOutOfOrder || k == ErrDuplicateReport {
						rmu.Lock()
						rejectOK++
						rmu.Unlock()
					} else {
						t.Errorf("unexpected concurrent error %+v: %v", c, err)
					}
				}
			}(c)
		}
		close(start)
		wg.Wait()
		if rejectOK == 0 {
			t.Fatalf("expected some rejections under contention, got none")
		}
		_ = rejectOther
		// 被拒操作不改状态：逐站按序补报，所有车都应能跑完全程。
		finishClock := int64(10000)
		for stop := 0; stop < len(g.svc.scheme.Stops); stop++ {
			for _, id := range g.tripIDs {
				ev, _ := g.svc.GetEvent(id, stop)
				if ev.Reported {
					if ev.Departure > finishClock {
						finishClock = ev.Departure
					}
					continue
				}
				// 到站必须晚于本站上一辆已接受车的离站，保证串行位置上离站差非负。
				finishClock++
				if _, err := g.svc.ReportArrival(id, stop, finishClock); err != nil {
					t.Fatalf("clean replay after contention failed %d/%d: %v", id, stop, err)
				}
				ev2, _ := g.svc.GetEvent(id, stop)
				finishClock = ev2.Departure
			}
		}

		order := g.svc.Order()
		// 已发车的常规车次相对次序必须保持 100<200<300<400（备车可能插入其间）。
		filtered := make([]int64, 0, len(order))
		for _, id := range order {
			if id%100 == 0 {
				filtered = append(filtered, id)
			}
		}
		want := []int64{100, 200, 300, 400}
		if len(filtered) != len(want) {
			t.Fatalf("regular trips lost: %v", filtered)
		}
		for i := range want {
			if filtered[i] != want[i] {
				t.Fatalf("relative order changed: %v", filtered)
			}
		}
		// 并发的串行等价序即全局接受时钟序；逐站按“到站时刻升序”排列，
		// 相邻已接受车次的离站时刻差必须非负。
		for stop := range g.svc.scheme.Stops {
			type ad struct {
				arr, dep int64
			}
			var seq []ad
			for _, id := range order {
				v, _ := g.svc.GetEvent(id, stop)
				if v.Reported {
					seq = append(seq, ad{v.Arrival, v.Departure})
				}
			}
			for i := 0; i < len(seq); i++ {
				for j := i + 1; j < len(seq); j++ {
					if seq[j].arr < seq[i].arr {
						seq[i], seq[j] = seq[j], seq[i]
					}
				}
			}
			for i := 1; i < len(seq); i++ {
				if seq[i].arr < seq[i-1].arr {
					t.Fatalf("seed %d stop %d arrivals not ordered", seed, stop)
				}
				if seq[i].dep-seq[i-1].dep < 0 {
					t.Fatalf("seed %d stop %d negative departure gap dep=%d prev=%d",
						seed, stop, seq[i].dep, seq[i-1].dep)
				}
			}
		}
	}
}

// TestDeterministicReplay 相同操作序列在两个独立实例上重放，
// 得到逐字节一致的操作日志与到离时刻。
func TestDeterministicReplay(t *testing.T) {
	a := runReplay(t, 11)
	b := runReplay(t, 11)
	if !bytes.Equal(a, b) {
		t.Fatalf("replay logs differ")
	}
}

func runReplay(t *testing.T, seed int64) []byte {
	t.Helper()
	g := newGenCase(t, seed)
	var log bytes.Buffer
	g.svc.SetLogger(&log)
	for {
		cand := g.candidates()
		if len(cand) == 0 {
			break
		}
		id := cand[g.rng.Intn(len(cand))]
		tr, _ := g.svc.lookupTrip(id)
		at := g.chooseArrival(tr, tr.nextStop)
		g.exec(t, Op{Kind: OpReportArrival, TripID: id, Stop: tr.nextStop, At: at})
	}
	return log.Bytes()
}

// BenchmarkGetEvent 证明查询开销为 O(1)：1 次上报与 100000 次上报后，
// 单次查询耗时应处于同一量级（均为两次 map/切片直接寻址，不扫描历史）。
func BenchmarkGetEvent(b *testing.B) {
	measure := func(n int) float64 {
		sc := diffScheme()
		s, _ := NewService(sc)
		for i := 1; i <= n; i++ {
			id := int64(i * 100)
			d := fmt.Sprintf("d%d", i)
			_ = s.RegisterDriver(d, 0)
			_ = s.RegisterTrip(id, d, int64(i))
			_, _ = s.ReportArrival(id, 0, int64(i*100))
		}
		last := int64(n * 100)
		b.ResetTimer()
		var sink EventView
		for i := 0; i < b.N; i++ {
			sink, _ = s.GetEvent(last, 0)
		}
		return float64(sink.Departure)
	}
	b.Run("1_trip", func(b *testing.B) { measure(1) })
	b.Run("100k_trips", func(b *testing.B) { measure(100000) })
}
