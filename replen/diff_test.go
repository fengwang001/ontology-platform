package replen_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/replen"
	"ontology/task"
)

// gen 生成一条随机操作。状态（库位是否存在、任务号）由传入的 naive 决定，
// 但对两边使用同一输入，拒绝路径也一并覆盖。
func gen(rng *rand.Rand, n *naive, opIdx int) op {
	locs := make([]string, 0, len(n.slots))
	for loc := range n.slots {
		locs = append(locs, loc)
	}
	var openIDs []int64
	var maxID int64
	for id, t := range n.tasks {
		if id > maxID {
			maxID = id
		}
		if t.status == 0 {
			openIDs = append(openIDs, id)
		}
	}

	const (
		kAddSlot = 0
		kReserve = 1
		kPick    = 2
		kDemand  = 3
		kConfirm = 4
		kCancel  = 5
	)
	roll := rng.Intn(100)
	var k int
	switch {
	case roll < 22:
		k = kAddSlot
	case roll < 42:
		k = kReserve
	case roll < 64:
		k = kPick
	case roll < 84:
		k = kDemand
	case roll < 92:
		k = kConfirm
	default:
		k = kCancel
	}

	// 早期偏向建库位与储备，避免大量不存在错误。
	if opIdx < 6 {
		k = []int{kAddSlot, kReserve, kPick, kAddSlot, kReserve, kDemand}[opIdx]
	}

	switch k {
	case kAddSlot:
		loc := fmt.Sprintf("L%d", rng.Intn(12))
		sku := fmt.Sprintf("S%d", rng.Intn(3))
		// 约 12% 生成非法参数验证拒绝次序。
		if rng.Intn(8) == 0 {
			return op{kind: "addslot", loc: loc, sku: sku,
				a: rng.Int63n(5) + 1, b: rng.Int63n(5), c: rng.Int63n(100) + 1,
				d: rng.Int63n(20) + 1, e: rng.Int63n(100)}
		}
		c := rng.Int63n(100) + 2
		mi := rng.Int63n(c-1) + 1
		ma := mi + rng.Int63n(c-mi) + 1
		if ma > c {
			ma = c
		}
		box := rng.Int63n(20) + 1
		oh := rng.Int63n(c + 1)
		return op{kind: "addslot", loc: loc, sku: sku, a: mi, b: ma, c: c, d: box, e: oh}
	case kReserve:
		sku := fmt.Sprintf("S%d", rng.Intn(3))
		q := rng.Int63n(40) + 1
		if rng.Intn(25) == 0 {
			q = 0 // 触发非法
		}
		return op{kind: "reserve", sku: sku, a: q}
	case kPick:
		var loc string
		if len(locs) > 0 && rng.Intn(5) != 0 {
			loc = locs[rng.Intn(len(locs))]
		} else {
			loc = fmt.Sprintf("L%d", rng.Intn(12))
		}
		// 多数取货不超过 onHand 附近，制造水位下降但兼顾库存不足拒绝。
		q := rng.Int63n(20) + 1
		return op{kind: "pick", loc: loc, a: q}
	case kDemand:
		var loc string
		if len(locs) > 0 && rng.Intn(5) != 0 {
			loc = locs[rng.Intn(len(locs))]
			sl := n.slots[loc]
			// need 在 onHand 上下波动，确保既覆盖无动作也覆盖升级/新建。
			base := sl.onHand + sl.min + n.inTransit(loc)
			need := base/2 + rng.Int63n(base+2*sl.c) + 1
			if need > sl.cap {
				need = rng.Int63n(sl.cap) + 1
			}
			return op{kind: "demand", loc: loc, a: need}
		}
		loc = fmt.Sprintf("L%d", rng.Intn(12))
		need := rng.Int63n(120) + 1
		return op{kind: "demand", loc: loc, a: need}
	case kConfirm:
		if len(openIDs) == 0 {
			return gen(rng, n, opIdx) // 重摇一条非任务操作
		}
		id := openIDs[rng.Intn(len(openIDs))]
		qty := n.tasks[id].qty
		// 多数合法（0..qty），少量超量。
		var actual int64
		if rng.Intn(8) == 0 {
			actual = qty + rng.Int63n(5) + 1
		} else {
			actual = rng.Int63n(qty + 1)
		}
		return op{kind: "confirm", a: id, b: actual}
	case kCancel:
		// 80% 对未完成任务，其余对不存在/终态任务。
		if len(openIDs) > 0 && rng.Intn(5) < 4 {
			id := openIDs[rng.Intn(len(openIDs))]
			return op{kind: "cancel", a: id}
		}
		id := maxID + rng.Int63n(3)
		if id < 1 {
			id = 1
		}
		return op{kind: "cancel", a: id}
	}
	return op{kind: "pick", loc: "L0", a: 1}
}

func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	const opsPerGroup = 40
	var acceptedTotal, rejectedTotal, createdTotal, upgradedTotal int
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g) + 1))
		nv := newNaive()
		e := replen.New()
		var logb strings.Builder
		fmt.Fprintf(&logb, "==== group %d seed=%d ====\n", g, g+1)
		for i := 0; i < opsPerGroup; i++ {
			o := gen(rng, nv, i)
			want := nv.run(o)
			got := engineRun(e, o)
			// 判定依据：先比操作级输出（错误类别/升级号/新建号），再比全量快照。
			if !sameResult(want, got) {
				fmt.Fprintf(&logb, "MISMATCH op=%s\n  naive=%+v\n  engine=%+v\n",
					o, want, got)
				t.Fatalf("group %d:\n%s", g, logb.String())
			}
			ns := nv.snapshot()
			es := engineSnapshot(e)
			if ns.slots != es.slots || ns.reserve != es.reserve || ns.tasks != es.tasks {
				fmt.Fprintf(&logb, "STATE MISMATCH op=%s\n naive slots=%s\n eng  slots=%s\n naive reserve=%s\n eng  reserve=%s\n naive tasks=%s\n eng  tasks=%s\n",
					o, ns.slots, es.slots, ns.reserve, es.reserve, ns.tasks, es.tasks)
				t.Fatalf("group %d:\n%s", g, logb.String())
			}
			switch {
			case want.err != "":
				rejectedTotal++
			default:
				acceptedTotal++
			}
			if want.created != 0 {
				createdTotal++
			}
			upgradedTotal += len(want.upgraded)
			fmt.Fprintf(&logb, "%s => err=%s upgraded=%v created=#%d | tasks=%s\n",
				o, want.err, want.upgraded, want.created, ns.tasks)
		}
		// 每组首组打印完整日志作为样例（输入/输出/判定依据）。
		if g == 0 || (g < 3 && testing.Verbose()) {
			t.Logf("%s", logb.String())
		}
	}
	t.Logf("differential done: groups=%d accepted=%d rejected=%d created=%d upgraded=%d",
		groups, acceptedTotal, rejectedTotal, createdTotal, upgradedTotal)
}

// TestTouchedBounds 100 与 10000 库位两档：Pick 及常规检查触碰 ≤ 3 条；
// Demand 触碰任务数 ≤ 该库位未完成任务数 + 1，且均与库位总数/SKU 任务总数无关。
func TestTouchedBounds(t *testing.T) {
	for _, nLocs := range []int{100, 10000} {
		name := fmt.Sprintf("locs=%d", nLocs)
		t.Run(name, func(t *testing.T) {
			e := replen.New()
			sku := "S1"
			// 建库位：min 较大，使初始不触发；再统一储备。
			for i := 0; i < nLocs; i++ {
				loc := fmt.Sprintf("L%05d", i)
				if err := e.AddSlot(loc, sku, 50, 80, 100, 10, 60); err != nil {
					t.Fatalf("addslot %s: %v", loc, err)
				}
			}
			// 分次加储备到较高水位。
			for r := 0; r < 100; r++ {
				if err := e.AddReserve(sku, 1_000_000_000); err != nil {
					t.Fatalf("reserve: %v", err)
				}
			}
			// Pick 触发常规：触碰应为 库位记录 1 + 任务记录 1 = 2。
			loc := "L00000"
			if err := e.Pick(loc, 11); err != nil { // onHand 49 <= 50
				t.Fatalf("pick: %v", err)
			}
			if tc := e.Touched(); tc > 3 {
				t.Fatalf("Pick+常规检查触碰 %d > 3 (nLocs=%d)", tc, nLocs)
			}
			if tc := e.Touched(); tc < 2 {
				t.Fatalf("Pick+常规检查应触碰至少 2 条, got %d", tc)
			}
			// 再多造两条该 SKU 的其它库位任务，确认与 SKU 任务总数无关。
			must(t, e.Pick("L00001", 11))
			must(t, e.Pick("L00002", 11))
			beforeOpen := len(e.Tasks())
			if err := e.Pick(loc, 1); err != nil { // 在途阻止重复触发
				t.Fatalf("pick2: %v", err)
			}
			if tc := e.Touched(); tc > 3 {
				t.Fatalf("在途 Pick 触碰 %d > 3 (open tasks=%d)", tc, beforeOpen)
			}

			// Demand：先把 loc 的在途清掉以便构造升级序列。
			must(t, e.Cancel(1)) // 取消 L0 的任务，可能连锁再建
			// 用极小 max 场景不易，直接对 L00001 做 Demand：其有 1 条常规可升级。
			openAt := func(l string) int {
				c := 0
				for _, tk := range e.Tasks() {
					if tk.Loc == l {
						c++
					}
				}
				return c
			}
			openBefore := openAt("L00001")
			// onHand=49，need=100：升级 1 条(30)后 49+30=79<100，再新建。
			_, err := e.Demand("L00001", 100)
			must(t, err)
			touched := e.Touched()
			if int64(touched) > int64(openBefore+1)+1 { // +1 库位记录，再 +1 新建任务
				t.Fatalf("Demand 触碰 %d > 库位未完成任务(%d)+1(+库位)", touched, openBefore)
			}
		})
	}
}

// TestConcurrentDeterminism 并发重放同一操作序列，最终任务快照一致；
// 且任何中间状态满足不变式（eff<=cap、avail>=0、任务量为 c 的正整数倍）。
func TestConcurrentDeterminism(t *testing.T) {
	plan := buildPlan()
	snapshots := make([]string, 6)
	var wg sync.WaitGroup
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func(rep int) {
			defer wg.Done()
			e := replen.New()
			for _, o := range plan {
				engineRun(e, o)
				assertInvariants(e)
			}
			snapshots[rep] = engineSnapshot(e).slots + "|" + engineSnapshot(e).tasks +
				"|" + engineSnapshot(e).reserve
		}(r)
	}
	wg.Wait()
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i] != snapshots[0] {
			t.Fatalf("并发重放结果不一致:\n%s\nvs\n%s", snapshots[0], snapshots[i])
		}
	}
}

func assertInvariants(e *replen.Engine) {
	for _, sl := range e.Slots() {
		if sl.OnHand+sl.InTransit > sl.Cap {
			panic(fmt.Sprintf("invariant eff>cap: %s oh=%d it=%d cap=%d",
				sl.Loc, sl.OnHand, sl.InTransit, sl.Cap))
		}
	}
	for _, r := range e.Reserves() {
		if r.Avail < 0 {
			panic(fmt.Sprintf("invariant avail<0: %s avail=%d", r.SKU, r.Avail))
		}
	}
	for _, tk := range e.Tasks() {
		sl := e.Slot(tk.Loc)
		if sl == nil || tk.Qty <= 0 || tk.Qty%sl.C != 0 {
			panic(fmt.Sprintf("invariant task qty not box multiple: #%d qty=%d",
				tk.ID, tk.Qty))
		}
	}
}

// buildPlan 构造一份覆盖各操作类型的固定计划（并发 goroutine 各自独立引擎执行）。
func buildPlan() []op {
	nv := newNaive()
	rng := rand.New(rand.NewSource(42))
	var ops []op
	for i := 0; i < 200; i++ {
		ops = append(ops, gen(rng, nv, i))
		nv.run(ops[i])
	}
	return ops
}

var _ = task.Regular

// TestConcurrentInterleaving 多 goroutine 对同一引擎交错调用：
// 只能断言不变式始终成立、操作错误类别合法、最终状态自洽（不规定具体顺序）。
func TestConcurrentInterleaving(t *testing.T) {
	e := replen.New()
	must(t, e.AddSlot("C0", "CS", 1, 80, 100, 5, 60))
	// 预建独立库位与共享 SKU 储备。
	const workers = 12
	for w := 0; w < workers; w++ {
		loc := fmt.Sprintf("C%02d", w)
		if w == 0 {
			continue
		}
		must(t, e.AddSlot(loc, "CS", 1, 80, 100, 5, 60))
	}
	for i := 0; i < 200; i++ {
		must(t, e.AddReserve("CS", 1_000_000_000))
	}

	validErr := func(err error) bool {
		return err == nil || errorsIsAny(err,
			replen.ErrInvalid, replen.ErrNotFound, replen.ErrState,
			replen.ErrConflict, replen.ErrShortPick, replen.ErrOverQty)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 7))
			loc := fmt.Sprintf("C%02d", w)
			for i := 0; i < 300; i++ {
				switch rng.Intn(4) {
				case 0:
					if err := e.Pick(loc, rng.Int63n(10)+1); !validErr(err) {
						t.Errorf("pick bad err: %v", err)
						return
					}
				case 1:
					if _, err := e.Demand(loc, rng.Int63n(100)+1); !validErr(err) {
						t.Errorf("demand bad err: %v", err)
						return
					}
				case 2:
					if ts := e.Tasks(); len(ts) > 0 {
						tk := ts[rng.Intn(len(ts))]
						if err := e.Confirm(tk.ID, rng.Int63n(tk.Qty+1)); !validErr(err) {
							t.Errorf("confirm bad err: %v", err)
							return
						}
					}
				default:
					if ts := e.Tasks(); len(ts) > 0 {
						tk := ts[rng.Intn(len(ts))]
						if err := e.Cancel(tk.ID); !validErr(err) {
							t.Errorf("cancel bad err: %v", err)
							return
						}
					}
				}
				assertInvariants(e)
			}
		}(w)
	}
	wg.Wait()
	assertInvariants(e)
}

func errorsIsAny(err error, targets ...error) bool {
	for _, tg := range targets {
		if errors.Is(err, tg) {
			return true
		}
	}
	return false
}
