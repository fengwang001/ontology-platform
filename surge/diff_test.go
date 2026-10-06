package surge

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"testing"
)

var diffLogPath = "/tmp/surge-diff.log"

var (
	diffLogMu sync.Mutex
	diffLogF  *os.File
)

// logStep 把每步输入/输出/判定依据写入日志文件与测试日志；
// 日志文件始终存在，便于离线复现。
func logStep(t *testing.T, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf(format, args...)
	t.Log(line)
	diffLogMu.Lock()
	defer diffLogMu.Unlock()
	if diffLogF != nil {
		fmt.Fprintln(diffLogF, line)
	}
}

// model 抽象两个实现的共同操作面，便于差分驱动。
type model interface {
	AddRegion(RegionID) error
	RiderOnline(TimeSec, RiderID, RegionID) error
	RiderOffline(TimeSec, RiderID) error
	RiderMove(TimeSec, RiderID, RegionID) error
	Evaluate(TimeSec, RegionID) (Tier, error)
	CreateOrder(TimeSec, OrderID, RegionID) error
	DispatchOrder(TimeSec, OrderID, RiderID) error
	CancelOrder(TimeSec, OrderID) error
	CompleteOrder(TimeSec, OrderID) (int64, error)
	Snapshot() Snapshot
}

type opKind int

const (
	opOnline opKind = iota
	opOffline
	opMove
	opEvaluate
	opCreate
	opDispatch
	opCancel
	opComplete
)

type genOp struct {
	kind   opKind
	at     TimeSec
	region RegionID
	rider  RiderID
	order  OrderID
}

func (o genOp) String() string {
	switch o.kind {
	case opOnline:
		return fmt.Sprintf("RiderOnline(t=%d rider=%s region=%s)", o.at, o.rider, o.region)
	case opOffline:
		return fmt.Sprintf("RiderOffline(t=%d rider=%s)", o.at, o.rider)
	case opMove:
		return fmt.Sprintf("RiderMove(t=%d rider=%s dest=%s)", o.at, o.rider, o.region)
	case opEvaluate:
		return fmt.Sprintf("Evaluate(t=%d region=%s)", o.at, o.region)
	case opCreate:
		return fmt.Sprintf("CreateOrder(t=%d order=%s region=%s)", o.at, o.order, o.region)
	case opDispatch:
		return fmt.Sprintf("DispatchOrder(t=%d order=%s rider=%s)", o.at, o.order, o.rider)
	case opCancel:
		return fmt.Sprintf("CancelOrder(t=%d order=%s)", o.at, o.order)
	default:
		return fmt.Sprintf("CompleteOrder(t=%d order=%s)", o.at, o.order)
	}
}

// runOp 在一个模型上执行操作，返回 (档位或补贴, 错误码, 错误)。
func runOp(m model, o genOp) (int64, ErrorCode, error) {
	switch o.kind {
	case opOnline:
		err := m.RiderOnline(o.at, o.rider, o.region)
		return 0, CodeOf(err), err
	case opOffline:
		err := m.RiderOffline(o.at, o.rider)
		return 0, CodeOf(err), err
	case opMove:
		err := m.RiderMove(o.at, o.rider, o.region)
		return 0, CodeOf(err), err
	case opEvaluate:
		v, err := m.Evaluate(o.at, o.region)
		return int64(v), CodeOf(err), err
	case opCreate:
		err := m.CreateOrder(o.at, o.order, o.region)
		return 0, CodeOf(err), err
	case opDispatch:
		err := m.DispatchOrder(o.at, o.order, o.rider)
		return 0, CodeOf(err), err
	case opCancel:
		err := m.CancelOrder(o.at, o.order)
		return 0, CodeOf(err), err
	default:
		v, err := m.CompleteOrder(o.at, o.order)
		return v, CodeOf(err), err
	}
}

// applyOp 按序应用于两个模型，并比较返回与全量快照。
// 每一步打印输入、输出与判定依据（始终输出到测试日志）。
func applyOp(t *testing.T, a, b model, o genOp, step int) {
	t.Helper()
	va, ca, ea := runOp(a, o)
	vb, cb, eb := runOp(b, o)
	reason := "ACCEPTED"
	if ea != nil {
		reason = fmt.Sprintf("REJECTED code=%s", ca)
	}
	logStep(t, "step=%d | input: %s | output: fast(value=%d) naive(value=%d) | verdict: %s",
		step, o, va, vb, reason)
	if ca != cb || (ea == nil && va != vb) {
		t.Fatalf("step %d diverged on result:\ninput=%s\nfast=(%d,%v)\nnaive=(%d,%v)",
			step, o, va, ea, vb, eb)
	}
	sa, sb := a.Snapshot(), b.Snapshot()
	if !reflect.DeepEqual(sa, sb) {
		t.Fatalf("step %d snapshot diverged:\ninput=%s\nfast=%#v\nnaive=%#v", step, o, sa, sb)
	}
	if ea == nil {
		assertInvariants(t, sa, o)
	}
}

// assertInvariants 校验题面三条恒等式与持单上限。
func assertInvariants(t *testing.T, snap Snapshot, after genOp) {
	t.Helper()
	// 持单上限。
	for id, rd := range snap.Riders {
		if rd.Held > testCfg().MaxHeld {
			t.Fatalf("invariant: rider %s held %d exceeds cap", id, rd.Held)
		}
	}
	// 可用运力数 == 区域内在线且持单未满骑手数。
	wantCap := map[RegionID]int{}
	for _, rd := range snap.Riders {
		if rd.Online && rd.Held < testCfg().MaxHeld {
			wantCap[rd.Region]++
		}
	}
	for reg, rs := range snap.Regions {
		if rs.Capacity != wantCap[reg] {
			t.Fatalf("invariant: region %s capacity %d != recomputed %d (after %s)",
				reg, rs.Capacity, wantCap[reg], after)
		}
	}
	// 待派订单数 == 已创建未派未取消订单数。
	wantPending := map[RegionID]int{}
	for _, od := range snap.Orders {
		if od.Stage == stagePending {
			wantPending[od.Region]++
		}
	}
	for reg, rs := range snap.Regions {
		if rs.Pending != wantPending[reg] {
			t.Fatalf("invariant: region %s pending %d != recomputed %d (after %s)",
				reg, rs.Pending, wantPending[reg], after)
		}
	}
}

func genSequence(rng *rand.Rand, n, regions, riders, orders int) []genOp {
	regionIDs := make([]RegionID, regions)
	for i := range regionIDs {
		regionIDs[i] = RegionID(fmt.Sprintf("R%d", i))
	}
	ops := []genOp{}
	now := TimeSec(0)
	pick := func(limit int) int { return rng.Intn(limit) }
	for i := 0; i < n; i++ {
		now += TimeSec(rng.Intn(3)) // 允许相等、也偶尔产生回退尝试？不：只增
		reg := regionIDs[pick(regions)]
		rid := RiderID(fmt.Sprintf("K%d", pick(riders)))
		oid := OrderID(fmt.Sprintf("D%d", pick(orders)))
		// 偶发显式回退时刻，验证两边都拒绝。
		at := now
		if rng.Intn(20) == 0 && len(ops) > 0 {
			at = now - TimeSec(1+rng.Intn(5))
		}
		var o genOp
		switch rng.Intn(8) {
		case 0:
			o = genOp{opOnline, at, reg, rid, ""}
		case 1:
			o = genOp{opOffline, at, "", rid, ""}
		case 2:
			o = genOp{opMove, at, reg, rid, ""}
		case 3:
			o = genOp{opEvaluate, at, reg, "", ""}
		case 4:
			o = genOp{opCreate, at, reg, "", oid}
		case 5:
			o = genOp{opDispatch, at, "", rid, oid}
		case 6:
			o = genOp{opCancel, at, "", "", oid}
		default:
			o = genOp{opComplete, at, "", "", oid}
		}
		ops = append(ops, o)
	}
	return ops
}

func runDifferential(t *testing.T, seed, steps int, verbose bool) {
	diffLogMu.Lock()
	if diffLogF == nil {
		f, ferr := os.Create(diffLogPath)
		if ferr == nil {
			diffLogF = f
		}
	}
	diffLogMu.Unlock()
	t.Cleanup(func() {
		diffLogMu.Lock()
		if diffLogF != nil {
			diffLogF.Sync()
		}
		diffLogMu.Unlock()
	})
	rng := rand.New(rand.NewSource(int64(seed)))
	cfg := testCfg()
	fast, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	naive, err := NewNaiveModel(cfg)
	if err != nil {
		t.Fatal(err)
	}
	regions := 1 + rng.Intn(3)
	ops := genSequence(rng, steps, regions, 4+rng.Intn(5), 6+rng.Intn(8))

	logStep(t, "seed=%d steps=%d regions=%d: registering regions first", seed, steps, regions)
	regionSet := map[RegionID]bool{}
	for _, o := range ops {
		if o.region != "" && !regionSet[o.region] {
			regionSet[o.region] = true
			ea := fast.AddRegion(o.region)
			eb := naive.AddRegion(o.region)
			if CodeOf(ea) != CodeOf(eb) {
				t.Fatalf("add region divergence: %v %v", ea, eb)
			}
		}
	}
	// 用相同的序列驱动两边。
	for i, o := range ops {
		applyOp(t, fast, naive, o, i)
	}
	// 事件与补贴账目的最终一致性。
	for reg := range regionSet {
		ea, _ := fast.TierEvents(reg)
		eb, _ := naive.TierEvents(reg)
		if len(ea) != len(eb) {
			t.Fatalf("tier events diverge for %s:\nfast=%v\nnaive=%v", reg, ea, eb)
		}
		for i := range ea {
			if ea[i] != eb[i] {
				t.Fatalf("tier event[%d] diverge for %s:\nfast=%v\nnaive=%v", i, reg, ea[i], eb[i])
			}
		}
	}
	for id := range fast.Snapshot().Riders {
		fa, _ := fast.SubsidyEntries(id)
		fb, _ := naive.SubsidyEntries(id)
		if len(fa) != len(fb) {
			t.Fatalf("subsidy entries diverge for %s: %v vs %v", id, fa, fb)
		}
		for i := range fa {
			if fa[i] != fb[i] {
				t.Fatalf("subsidy entry[%d] diverge for %s: %v vs %v", i, id, fa[i], fb[i])
			}
		}
	}
}

// TestDifferentialShort 小规模差分并逐步打印每步输入/输出/判定依据。
func TestDifferentialShort(t *testing.T) {
	runDifferential(t, 42, 60, true)
}

// TestDifferentialRandomized 多随机种子、更长序列与朴素全量重算对照。
// 设置 SURGE_DIFF_LOG=1 可打印全部步骤（默认只在失败时输出）。
func TestDifferentialRandomized(t *testing.T) {
	if os.Getenv("SURGE_DIFF_LOG") == "" {
		t.Skip("set SURGE_DIFF_LOG=1 for full per-step logging")
	}
	for seed := 0; seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed*97+1, 400, true)
		})
	}
}

// TestDifferentialQuick 不带日志环境变量时也始终运行的快速差分。
func TestDifferentialQuick(t *testing.T) {
	for seed := 0; seed < 12; seed++ {
		runDifferential(t, seed*131+7, 250, false)
	}
}

// TestReplayDeterminism 相同操作序列在两个全新正式实例上重放，
// 必须得到相同档位事件与补贴账目。
func TestReplayDeterminism(t *testing.T) {
	build := func() (*System, []genOp) {
		s, _ := NewSystem(testCfg())
		ops := []genOp{}
		_ = s.AddRegion("A")
		_ = s.AddRegion("B")
		for i := 0; i < 40; i++ {
			reg := RegionID("A")
			if i%3 == 0 {
				reg = "B"
			}
			t := TimeSec(i)
			switch i % 6 {
			case 0:
				ops = append(ops, genOp{opOnline, t, reg, RiderID(fmt.Sprintf("K%d", i%5)), ""})
			case 1:
				ops = append(ops, genOp{opCreate, t, reg, "", OrderID(fmt.Sprintf("D%d", i))})
			case 2:
				ops = append(ops, genOp{opEvaluate, t, reg, "", ""})
			case 3:
				ops = append(ops, genOp{opDispatch, t, "", RiderID(fmt.Sprintf("K%d", i%5)), OrderID(fmt.Sprintf("D%d", i-2))})
			case 4:
				ops = append(ops, genOp{opComplete, t, "", "", OrderID(fmt.Sprintf("D%d", i-3))})
			default:
				ops = append(ops, genOp{opMove, t, "B", RiderID(fmt.Sprintf("K%d", i%5)), ""})
			}
		}
		return s, ops
	}
	s1, ops := build()
	s2, _ := build()
	for _, o := range ops {
		v1, c1, e1 := runOp(s1, o)
		v2, c2, e2 := runOp(s2, o)
		if c1 != c2 || v1 != v2 {
			t.Fatalf("replay diverge at %s: (%d,%v) vs (%d,%v)", o, v1, e1, v2, e2)
		}
	}
	if !reflect.DeepEqual(s1.Snapshot(), s2.Snapshot()) {
		t.Fatalf("replay snapshots differ")
	}
}
