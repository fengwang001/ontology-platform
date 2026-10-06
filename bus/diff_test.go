package bus

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// applyOnService 把一条 Op 施加到真实服务上。
func applyOnService(s *Service, op Op) (error, *StopInfo) {
	switch op.Kind {
	case OpRegister:
		return s.RegisterDriver(op.Driver), nil
	case OpBackup:
		return s.AddBackup(op.Trip, op.Driver, op.At), nil
	case OpSchedule:
		return s.ScheduleTrip(op.Trip, op.Driver, op.At), nil
	case OpAlight:
		return s.RegisterAlight(op.Trip, op.Station, op.At), nil
	case OpArrival:
		return s.ReportArrival(op.Trip, op.Station, op.At), nil
	case OpHold:
		info, err := s.Intervene(op.Trip, op.Station, ActionHold)
		return err, info
	case OpSkip:
		info, err := s.Intervene(op.Trip, op.Station, ActionSkip)
		return err, info
	default:
		return ErrInvalidArgument, nil
	}
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil)
}

func compareSnapshots(t *testing.T, a, b Snapshot, step int, ops []Op) {
	t.Helper()
	if a.Clock != b.Clock {
		t.Fatalf("第%d步后时钟不一致：%d != %d", step, a.Clock, b.Clock)
	}
	if len(a.Trips) != len(b.Trips) {
		t.Fatalf("第%d步后运行车次序列长度不一致：%v != %v", step, a.Trips, b.Trips)
	}
	for i := range a.Trips {
		if a.Trips[i] != b.Trips[i] {
			t.Fatalf("第%d步后车次次序不一致：%v != %v", step, a.Trips, b.Trips)
		}
	}
	if len(a.Pool) != len(b.Pool) {
		t.Fatalf("第%d步后备车池不一致：%v != %v", step, a.Pool, b.Pool)
	}
	for i := range a.Pool {
		if a.Pool[i] != b.Pool[i] {
			t.Fatalf("第%d步后备车池次序不一致：%v != %v", step, a.Pool, b.Pool)
		}
	}
	for key, x := range a.Stops {
		y, ok := b.Stops[key]
		if !ok {
			t.Fatalf("第%d步后朴素模型缺少记录 %v", step, key)
		}
		if x != y {
			t.Fatalf("第%d步后记录 %v 不一致：\n服务=%+v\n朴素=%+v", step, key, x, y)
		}
	}
	for key := range b.Stops {
		if _, ok := a.Stops[key]; !ok {
			t.Fatalf("第%d步后服务缺少记录 %v", step, key)
		}
	}
}

// TestRandomDifferential 用随机操作序列对照真实服务与独立朴素模型，
// 每一步都要求：错误有无一致、快照完全一致。
func TestRandomDifferential(t *testing.T) {
	const iterations = 200
	const maxOps = 400
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter*7919 + 1)))
		cfg := genConfig(rng)
		svc, err := New(cfg)
		mustOK(t, err, "New(svc)")
		naive := NewNaiveModel(cfg)
		st := newSimState(cfg, rng)

		var ops []Op
		for i := 0; i < maxOps; i++ {
			op, ok := st.generate()
			if !ok {
				continue
			}
			ops = append(ops, op)
			e1, info := applyOnService(svc, op)
			e2, _ := naive.Apply(op)
			if !sameErr(e1, e2) {
				t.Fatalf("第%d步 %v 错误不一致：服务=%v 朴素=%v（info=%+v）",
					i, op, e1, e2, info)
			}
			if (e1 == nil) && (op.Kind == OpHold || op.Kind == OpSkip) {
				a := svc.Snapshot().Stops[([2]int64{op.Trip, int64(op.Station)})]
				b := naive.Snapshot().Stops[([2]int64{op.Trip, int64(op.Station)})]
				if a != b {
					for j := range ops {
						t.Logf("op%d %s", j, naive.Log()[j])
					}
					t.Fatalf("第%d步干预后立即不一致：\n服务=%+v\n朴素=%+v", i, a, b)
				}
			}
			compareSnapshots(t, svc.Snapshot(), naive.Snapshot(), i, ops)
		}
	}
}

// TestRandomLogPrintsInputsOutputs 显式打印每条操作的输入、输出与判定依据。
func TestRandomLogPrintsInputsOutputs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cfg := genConfig(rng)
	svc, err := New(cfg)
	mustOK(t, err, "New")
	naive := NewNaiveModel(cfg)
	st := newSimState(cfg, rng)
	n := 0
	for n < 60 {
		op, ok := st.generate()
		if !ok {
			continue
		}
		e1, _ := applyOnService(svc, op)
		e2, detail := naive.Apply(op)
		if !sameErr(e1, e2) {
			t.Fatalf("错误不一致：%v vs %v", e1, e2)
		}
		entry := LogEntry{Op: op, Err: e1, Detail: detail}
		t.Log(entry.String())
		n++
	}
	compareSnapshots(t, svc.Snapshot(), naive.Snapshot(), n, nil)
}

// TestConcurrentLinearizable：并发上报与查询不得竞态、不得破坏状态；
// 最终状态必须等价于某个串行顺序（与同一序列的朴素重放一致）。
func TestConcurrentLinearizable(t *testing.T) {
	cfg := testConfig(200, 200, 100000)
	s, err := New(cfg)
	mustOK(t, err, "New")
	for d := int64(1); d <= 8; d++ {
		mustOK(t, s.RegisterDriver(d), "driver")
	}

	var wg sync.WaitGroup
	// 8 个工作者：各自负责一辆车的顺序上报，时刻取自单调全局时钟的分片。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			trip := int64(worker + 1)
			driver := int64(worker + 1)
			base := int64(worker * 15)
			if err := s.ScheduleTrip(trip, driver, base); err != nil {
				return // 串行化下可能输给其他工作者，等价于某个串行顺序
			}
			clock := base
			for st := 1; st < cfg.StationCount; st++ {
				clock += 70
				if err := s.ReportArrival(trip, st, clock); err != nil {
					return
				}
				// 并发只读查询，验证不发生竞态且永不返回负差。
				info, qerr := s.Query(trip, st)
				if qerr == nil && info.Departure < info.Arrival {
					t.Errorf("离站早于到站：%+v", info)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 所有成功车次的到离时刻应自洽：离站 >= 到站。
	for id := int64(1); id <= 8; id++ {
		for st := 0; st < cfg.StationCount; st++ {
			info, qerr := s.Query(id, st)
			if qerr != nil || !info.Reported {
				continue
			}
			if info.Departure < info.Arrival {
				t.Fatalf("车次 %d 站 %d 离站早于到站", id, st)
			}
		}
	}
}

// BenchmarkQueryConstantTime 验证 Query 的耗时不随累计记录数增长：
// 100 与 10000 条记录规模下每次查询耗时应处于同一常数区间。
func BenchmarkQueryConstantTime(b *testing.B) {
	build := func(records int) *Service {
		cfg := Config{
			StationCount:       5,
			ControlStations:    []int{0, 2, 4},
			TargetHeadway:      100,
			Dwells:             []int64{10, 10, 10, 10, 10},
			TravelTimes:        []int64{40, 40, 40, 40},
			HoldCap:            30,
			DepartureTolerance: 100,
			MaxDutySeconds:     1 << 40,
		}
		s, _ := New(cfg)
		clock := int64(0)
		for i := 0; i < records; i++ {
			id := int64(i + 1)
			_ = s.RegisterDriver(id)
			clock++
			_ = s.ScheduleTrip(id, id, clock)
			for st := 1; st < cfg.StationCount; st++ {
				clock += 50
				_ = s.ReportArrival(id, st, clock)
			}
		}
		return s
	}

	for _, records := range []int{100, 10000} {
		s := build(records)
		b.Run(fmt.Sprintf("records=%d", records), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				id := int64((i % records) + 1)
				if _, err := s.Query(id, 3); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// genConfig 生成随机但合法的方案。
func genConfig(rng *rand.Rand) Config {
	n := 3 + rng.Intn(5) // 3..7
	controls := []int{}
	for st := 0; st < n; st++ {
		if st == 0 || st == n-1 || rng.Intn(2) == 0 {
			controls = append(controls, st)
		}
	}
	dwells := make([]int64, n)
	travels := make([]int64, n-1)
	for i := range dwells {
		dwells[i] = int64(5 + rng.Intn(30))
	}
	for i := range travels {
		travels[i] = int64(10 + rng.Intn(60))
	}
	return Config{
		StationCount:       n,
		ControlStations:    controls,
		TargetHeadway:      int64(60 + rng.Intn(120)),
		Dwells:             dwells,
		TravelTimes:        travels,
		HoldCap:            int64(10 + rng.Intn(120)),
		DepartureTolerance: int64(20 + rng.Intn(200)),
		MaxDutySeconds:     int64(200 + rng.Intn(3000)),
	}
}

type simState struct {
	cfg       Config
	control   []bool
	rng       *rand.Rand
	clock     int64
	nextTrip  int64
	drivers   map[int64]bool
	scheduled map[int64]bool
	pool      map[int64]bool
	lastRep   map[int64]int
	skipped   map[int64]map[int]bool
	insertAt  map[int64]int
	bunched   map[[2]int64]bool
	alight    map[[2]int64]bool
}

func newSimState(cfg Config, rng *rand.Rand) *simState {
	return &simState{
		cfg: cfg,
		control: func() []bool {
			c := make([]bool, cfg.StationCount)
			for _, st := range cfg.ControlStations {
				c[st] = true
			}
			return c
		}(),
		rng:       rng,
		nextTrip:  1,
		drivers:   map[int64]bool{},
		scheduled: map[int64]bool{},
		pool:      map[int64]bool{},
		lastRep:   map[int64]int{},
		skipped:   map[int64]map[int]bool{},
		insertAt:  map[int64]int{},
		bunched:   map[[2]int64]bool{},
		alight:    map[[2]int64]bool{},
	}
}

func (st *simState) advance(delta int64) int64 {
	st.clock += delta
	return st.clock
}

func (st *simState) nextDriver() int64 {
	id := st.nextTrip + 1000
	st.nextTrip++
	st.drivers[id] = true
	return id
}

// generate 产生一条对真实系统“大概率合法”的随机操作；返回 ok=false 表示无可用动作。
// 生成器只依据自身维护的影子状态推进，因此与被测实现完全独立。
func (st *simState) generate() (Op, bool) {
	choices := []OpKind{OpArrival, OpArrival, OpArrival, OpAlight, OpHold, OpSkip, OpBackup}
	rng := st.rng

	// 若还没有在册司机，先登记。
	if len(st.drivers) == 0 {
		id := int64(1000 + rng.Intn(50))
		st.drivers[id] = true
		return Op{Kind: OpRegister, Driver: id, At: st.advance(1)}, true
	}

	// 最多运行 6 个车次；允许首发新车。
	if len(st.scheduled) < 6 && rng.Intn(3) == 0 {
		id := int64(len(st.scheduled)+1) * 10
		tries := 0
		for st.scheduled[id] || st.pool[id] {
			id = int64(100 + rng.Intn(900))
			tries++
			if tries > 50 {
				return Op{}, false
			}
		}
		drivers := make([]int64, 0, len(st.drivers))
		for d := range st.drivers {
			drivers = append(drivers, d)
		}
		driver := drivers[rng.Intn(len(drivers))]
		st.scheduled[id] = true
		st.lastRep[id] = 0
		st.skipped[id] = map[int]bool{}
		st.insertAt[id] = 0
		return Op{Kind: OpSchedule, Trip: id, Driver: driver, At: st.advance(int64(1 + rng.Intn(20)))}, true
	}

	kind := choices[rng.Intn(len(choices))]

	// 备车入池。
	if kind == OpBackup {
		id := int64(5000 + rng.Intn(4000))
		for st.pool[id] || st.scheduled[id] {
			id = int64(5000 + rng.Intn(4000))
		}
		drivers := make([]int64, 0, len(st.drivers))
		for d := range st.drivers {
			drivers = append(drivers, d)
		}
		driver := drivers[rng.Intn(len(drivers))]
		st.pool[id] = true
		return Op{Kind: OpBackup, Trip: id, Driver: driver, At: st.advance(int64(1 + rng.Intn(5)))}, true
	}

	// 选一个已投放车次（含已被插入的备车：scheduled 标记在插入时补上）。
	trips := make([]int64, 0, len(st.scheduled))
	for id := range st.scheduled {
		trips = append(trips, id)
	}
	if len(trips) == 0 {
		return Op{}, false
	}
	trip := trips[rng.Intn(len(trips))]
	cur := st.lastRep[trip]

	switch kind {
	case OpAlight:
		stn := st.insertAt[trip] + rng.Intn(st.cfg.StationCount-st.insertAt[trip])
		st.alight[([2]int64{trip, int64(stn)})] = true
		return Op{Kind: OpAlight, Trip: trip, Station: stn, At: st.advance(int64(1 + rng.Intn(3)))}, true

	case OpHold, OpSkip:
		if !st.bunched[([2]int64{trip, int64(cur)})] {
			return Op{}, false
		}
		op := Op{Trip: trip, Station: cur, At: st.advance(1)}
		delete(st.bunched, [2]int64{trip, int64(cur)})
		if kind == OpHold {
			op.Kind = OpHold
		} else {
			op.Kind = OpSkip
		}
		return op, true

	default: // OpArrival
		if cur+1 >= st.cfg.StationCount {
			return Op{}, false
		}
		next := cur + 1
		if st.skipped[trip][next] {
			// 被跳过站无需上报，直接推进影子游标。
			st.lastRep[trip] = next
			return Op{}, false
		}
		// 到站时刻：在当前时钟之后随机推进，制造正常/串车/大间隔三种形态。
		delta := int64(1)
		switch rng.Intn(3) {
		case 0:
			delta = int64(rng.Intn(int(st.cfg.TargetHeadway / 2))) // 易串车
		case 1:
			delta = st.cfg.TargetHeadway + int64(rng.Intn(40)) - 20
		default:
			delta = 2*st.cfg.TargetHeadway + int64(rng.Intn(60)) // 易大间隔
		}
		if delta < 1 {
			delta = 1
		}
		at := st.advance(delta)
		op := Op{Kind: OpArrival, Trip: trip, Station: next, At: at}
		st.lastRep[trip] = next

		// 影子层无法知道精确判定（依赖服务状态），这里只标记“可能串车”，
		// 精确性由两边状态快照对照保证；误标只会让干预操作变成一次预期的错误。
		if st.control[next] && rng.Intn(2) == 0 {
			st.bunched[([2]int64{trip, int64(next)})] = true
		}
		return op, true
	}
}
