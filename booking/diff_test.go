package booking

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

// opKind 枚举差分序列中的操作。
type opKind int

const (
	opPlace opKind = iota
	opReschedule
	opCancel
	opDeliver
	opSetCapacity
	opQueryOrder
	opQuerySlots
)

type op struct {
	kind     opKind
	now      int64
	orderID  string
	region   string
	start    int64
	capacity int
	accept   bool
}

// why 给出该操作的判定依据（人类可读，也写入日志）。
func (o op) why(cfg Config) string {
	switch o.kind {
	case opPlace:
		return fmt.Sprintf("place start=%d lead=[%d,%d] accept=%v",
			o.start, o.start-o.now, o.start-o.now, o.accept)
	case opReschedule:
		return fmt.Sprintf("reschedule %s -> %d deadlineAt=start-%d",
			o.orderID, o.start, cfg.RescheduleLead)
	case opCancel:
		return "cancel (allowed before/after release)"
	case opDeliver:
		return "deliver requires released(now >= start-DispatchLead)"
	case opSetCapacity:
		return fmt.Sprintf("setCapacity start=%d cap=%d (lower may overbook)", o.start, o.capacity)
	case opQueryOrder:
		return "query order: release is pure function of now"
	default:
		return "query slots"
	}
}

func (o op) String() string {
	switch o.kind {
	case opPlace:
		return fmt.Sprintf("Place(now=%d id=%s region=%s start=%d accept=%v)", o.now, o.orderID, o.region, o.start, o.accept)
	case opReschedule:
		return fmt.Sprintf("Reschedule(now=%d id=%s region=%s start=%d)", o.now, o.orderID, o.region, o.start)
	case opCancel:
		return fmt.Sprintf("Cancel(now=%d id=%s)", o.now, o.orderID)
	case opDeliver:
		return fmt.Sprintf("Deliver(now=%d id=%s)", o.now, o.orderID)
	case opSetCapacity:
		return fmt.Sprintf("SetCapacity(now=%d region=%s start=%d cap=%d)", o.now, o.region, o.start, o.capacity)
	case opQueryOrder:
		return fmt.Sprintf("QueryOrder(now=%d id=%s)", o.now, o.orderID)
	default:
		return fmt.Sprintf("QuerySlots(now=%d region=%s)", o.now, o.region)
	}
}

// result 捕获两个模型可比较的输出。
type result struct {
	errCode   ErrorCode
	place     *PlaceResult
	cancelAft *bool
	order     *OrderInfo
	slots     []SlotInfo
}

func runOnScheduler(s *Scheduler, o op) result {
	var r result
	switch o.kind {
	case opPlace:
		p, err := s.Place(o.now, PlaceRequest{OrderID: o.orderID, Region: o.region, SlotStart: o.start, AcceptPostpone: o.accept})
		r.errCode = CodeOf(err)
		if err == nil {
			r.place = &p
		}
	case opReschedule:
		r.errCode = CodeOf(s.Reschedule(o.now, o.orderID, o.region, o.start))
	case opCancel:
		a, err := s.Cancel(o.now, o.orderID)
		r.errCode = CodeOf(err)
		if err == nil {
			r.cancelAft = &a
		}
	case opDeliver:
		r.errCode = CodeOf(s.Deliver(o.now, o.orderID))
	case opSetCapacity:
		r.errCode = CodeOf(s.SetCapacity(o.now, o.region, o.start, o.capacity))
	case opQueryOrder:
		inf, err := s.QueryOrder(o.now, o.orderID)
		r.errCode = CodeOf(err)
		if err == nil {
			r.order = &inf
		}
	case opQuerySlots:
		sl, err := s.QuerySlots(o.now, o.region, 0, 1<<40)
		r.errCode = CodeOf(err)
		if err == nil {
			r.slots = sl
		}
	}
	return r
}

func runOnNaive(m *naiveModel, o op) result {
	var r result
	switch o.kind {
	case opPlace:
		p, err := m.place(o.now, PlaceRequest{OrderID: o.orderID, Region: o.region, SlotStart: o.start, AcceptPostpone: o.accept})
		r.errCode = CodeOf(err)
		if err == nil {
			r.place = &p
		}
	case opReschedule:
		r.errCode = CodeOf(m.reschedule(o.now, o.orderID, o.region, o.start))
	case opCancel:
		a, err := m.cancel(o.now, o.orderID)
		r.errCode = CodeOf(err)
		if err == nil {
			r.cancelAft = &a
		}
	case opDeliver:
		r.errCode = CodeOf(m.deliver(o.now, o.orderID))
	case opSetCapacity:
		r.errCode = CodeOf(m.setCapacity(o.now, o.region, o.start, o.capacity))
	case opQueryOrder:
		inf, err := m.queryOrder(o.now, o.orderID)
		r.errCode = CodeOf(err)
		if err == nil {
			r.order = &inf
		}
	case opQuerySlots:
		sl, err := m.querySlots(o.now, o.region, 0, 1<<40)
		r.errCode = CodeOf(err)
		if err == nil {
			r.slots = sl
		}
	}
	return r
}

func sameResult(a, b result) bool {
	if a.errCode != b.errCode {
		return false
	}
	if a.place != nil {
		return b.place != nil && *a.place == *b.place
	}
	if a.cancelAft != nil {
		return b.cancelAft != nil && *a.cancelAft == *b.cancelAft
	}
	if a.order != nil {
		return b.order != nil && *a.order == *b.order
	}
	if a.slots != nil {
		if b.slots == nil || len(a.slots) != len(b.slots) {
			return false
		}
		for i := range a.slots {
			if a.slots[i] != b.slots[i] {
				return false
			}
		}
	}
	return true
}

// genWorld 固定一套区域/时段骨架，供两个模型在序列开始前装载。
func genWorld(t *testing.T, s *Scheduler, m *naiveModel, rng *rand.Rand) {
	t.Helper()
	regions := []string{"R0", "R1"}
	for _, rg := range regions {
		if err := s.AddRegion(rg); err != nil {
			t.Fatal(err)
		}
		if err := m.addRegion(rg); err != nil {
			t.Fatal(err)
		}
		for st := int64(1000); st <= 20000; st += 1000 {
			capc := 1 + rng.Intn(4)
			if err := s.AddSlot(rg, st, st+600, capc); err != nil {
				t.Fatal(err)
			}
			if err := m.addSlot(rg, st, st+600, capc); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// genOps 生成单调时钟下的随机操作；约 5% 概率故意制造时钟回退。
func genOps(rng *rand.Rand, n int) []op {
	regions := []string{"R0", "R1"}
	starts := []int64{1000, 2000, 3000, 4000, 5000, 6000, 8000, 12000, 20000}
	ops := make([]op, 0, n)
	clock := int64(0)
	nextID := 0
	known := []string{}
	pickStart := func() int64 { return starts[rng.Intn(len(starts))] }
	for i := 0; i < n; i++ {
		if rng.Intn(20) == 0 && clock > 0 {
			ops = append(ops, op{kind: opKind(rng.Intn(4)), now: clock - int64(1+rng.Intn(5))})
			continue
		}
		clock += int64(rng.Intn(120))
		rg := regions[rng.Intn(len(regions))]
		kind := opKind(rng.Intn(int(opQuerySlots) + 1))
		o := op{kind: kind, now: clock, region: rg}
		switch kind {
		case opPlace:
			nextID++
			o.orderID = "ord" + strconvItoa(nextID)
			o.start = pickStart()
			o.accept = rng.Intn(2) == 0
			known = append(known, o.orderID)
		case opReschedule, opCancel, opDeliver, opQueryOrder:
			if len(known) == 0 {
				o.kind = opPlace
				nextID++
				o.orderID = "ord" + strconvItoa(nextID)
				o.start = pickStart()
				o.accept = true
				known = append(known, o.orderID)
			} else {
				o.orderID = known[rng.Intn(len(known))]
				if kind == opReschedule {
					o.start = pickStart()
				}
			}
		case opSetCapacity:
			o.start = pickStart()
			o.capacity = rng.Intn(6) // 0..5，常制造超额
		case opQuerySlots:
			// region 已设
		}
		ops = append(ops, o)
	}
	return ops
}

func strconvItoa(i int) string { return strconv.Itoa(i) }

// TestRandomDifferential 以固定种子生成序列，双模型逐步比对，
// 并打印每步输入、输出与判定依据；同时重放校验确定性。
func TestRandomDifferential(t *testing.T) {
	cfg := testConfig()
	seeds := []int64{1, 2, 3, 42, 100}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ops := genOps(rng, 600)

			var log bytes.Buffer
			s1, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m1 := newNaive(cfg)
			genWorld(t, s1, m1, rand.New(rand.NewSource(seed*7)))

			for i, o := range ops {
				rs := runOnScheduler(s1, o)
				rm := runOnNaive(m1, o)
				fmt.Fprintf(&log, "step=%d | %s | why: %s | model={code=%d %s} naive={code=%d %s}\n",
					i, o, o.why(cfg), rs.errCode, fmtResult(rs), rm.errCode, fmtResult(rm))
				if !sameResult(rs, rm) {
					t.Fatalf("seed=%d step=%d mismatch\n%s\n--- full log ---\n%s", seed, i, o, log.String())
				}
			}
			t.Logf("\n%s", log.String())

			// 重放：新建一对模型重放同一序列，落位与顺延记录必须一致。
			s2, _ := New(cfg)
			m2 := newNaive(cfg)
			genWorld(t, s2, m2, rand.New(rand.NewSource(seed*7)))
			for i, o := range ops {
				if !sameResult(runOnScheduler(s2, o), runOnNaive(m2, o)) {
					t.Fatalf("replay mismatch at step %d", i)
				}
			}
		})
	}
}

func fmtResult(r result) string {
	switch {
	case r.place != nil:
		return fmt.Sprintf("place(start=%d postponed=%v orig=%d)", r.place.SlotStart, r.place.Postponed, r.place.OrigSlot)
	case r.cancelAft != nil:
		return fmt.Sprintf("cancel(afterRelease=%v)", *r.cancelAft)
	case r.order != nil:
		return fmt.Sprintf("order(start=%d released=%v c=%v d=%v post=%v)",
			r.order.SlotStart, r.order.Released, r.order.Canceled, r.order.Delivered, r.order.Postponed)
	case r.slots != nil:
		return fmt.Sprintf("slots=%d", len(r.slots))
	default:
		return "-"
	}
}
