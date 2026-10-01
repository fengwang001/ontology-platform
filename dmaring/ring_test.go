package dmaring

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func slotsEqual(a []Slot, b []naiveSlot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].OWN != b[i].OWN || a[i].FIRST != b[i].FIRST ||
			a[i].LAST != b[i].LAST || a[i].Len != b[i].Len ||
			a[i].St != b[i].St {
			return false
		}
	}
	return true
}

func checkAgree(t *testing.T, tag string, r *Ring, m *naiveRing) {
	t.Helper()
	snap := r.Snapshot()
	ok := snap.Prod == m.prod && snap.Dev == m.dev && snap.Reap == m.reap &&
		snap.Free == m.free() && slotsEqual(snap.Slots, m.slots)
	if !ok {
		t.Fatalf("[%s] real vs naive mismatch:\nreal  prod=%d dev=%d reap=%d free=%d slots=%+v\nnaive prod=%d dev=%d reap=%d free=%d slots=%+v",
			tag, snap.Prod, snap.Dev, snap.Reap, snap.Free, snap.Slots,
			m.prod, m.dev, m.reap, m.free(), m.slots)
	}
}

func checkInvariants(t *testing.T, tag string, s Snapshot) {
	t.Helper()
	if !(s.Reap <= s.Dev && s.Dev <= s.Prod) {
		t.Fatalf("[%s] pointer order broken: reap=%d dev=%d prod=%d", tag, s.Reap, s.Dev, s.Prod)
	}
	if s.Prod-s.Reap > uint64(s.N) {
		t.Fatalf("[%s] occupancy exceeds N", tag)
	}
	if s.Free != s.N-int(s.Prod-s.Reap) {
		t.Fatalf("[%s] free formula wrong: free=%d prod-reap=%d", tag, s.Free, s.Prod-s.Reap)
	}
	owned := 0
	for _, sl := range s.Slots {
		if sl.OWN == 1 {
			owned++
		}
	}
	if owned != int(s.Prod-s.Dev) {
		t.Fatalf("[%s] OWN count %d != prod-dev %d", tag, owned, s.Prod-s.Dev)
	}
}

func runModels(t *testing.T, n int, build func(*testing.T, *Ring, *naiveRing)) {
	t.Helper()
	real, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	build(t, real, newNaive(n))
}

func diffSubmit(t *testing.T, tag string, r *Ring, m *naiveRing, lens []int64) {
	t.Helper()
	errR := r.Submit(lens)
	errM := m.naiveSubmit(lens)
	t.Logf("  IN/OUT %-26s Submit(%v) -> err=%v | naive err=%v", tag, lens, errR, errM)
	if errR != errM {
		t.Fatalf("[%s] submit err real=%v naive=%v", tag, errR, errM)
	}
	checkAgree(t, tag, r, m)
	checkInvariants(t, tag, r.Snapshot())
}

func diffRun(t *testing.T, tag string, r *Ring, m *naiveRing, k int) {
	t.Helper()
	uR, errR := r.DeviceRun(k)
	uM, errM := m.naiveDeviceRun(k)
	t.Logf("  IN/OUT %-26s DeviceRun(%d) -> units=%d err=%v | naive units=%d err=%v", tag, k, uR, errR, uM, errM)
	if errR != errM || uR != uM {
		t.Fatalf("[%s] run real=(%d,%v) naive=(%d,%v)", tag, uR, errR, uM, errM)
	}
	checkAgree(t, tag, r, m)
	checkInvariants(t, tag, r.Snapshot())
}

func diffFault(t *testing.T, tag string, r *Ring, m *naiveRing, seq uint64) {
	t.Helper()
	errR := r.Fault(seq)
	errM := m.naiveFault(seq)
	t.Logf("  IN/OUT %-26s Fault(%d) -> err=%v | naive err=%v", tag, seq, errR, errM)
	if errR != errM {
		t.Fatalf("[%s] fault real=%v naive=%v", tag, errR, errM)
	}
	checkAgree(t, tag, r, m)
}

func diffReap(t *testing.T, tag string, r *Ring, m *naiveRing) {
	t.Helper()
	resR, errR := r.Reap()
	resM, errM := m.naiveReap()
	t.Logf("  IN/OUT %-26s Reap() -> %+v err=%v | naive %+v err=%v", tag, resR, errR, resM, errM)
	if errR != errM || resR != resM {
		t.Fatalf("[%s] reap real=(%+v,%v) naive=(%+v,%v)", tag, resR, errR, resM, errM)
	}
	checkAgree(t, tag, r, m)
	checkInvariants(t, tag, r.Snapshot())
}

func TestNewValidation(t *testing.T) {
	for _, n := range []int{0, 1, 3, 5, 6, -4, 1023} {
		if _, err := New(n); err != ErrBadN {
			t.Fatalf("New(%d) err=%v want ErrBadN", n, err)
		}
		t.Logf("  IN/OUT New(%d) -> ErrBadN; 判断依据: N 非 2 的幂或 < 2", n)
	}
	for _, n := range []int{2, 4, 8, 1024} {
		r, err := New(n)
		if err != nil {
			t.Fatalf("New(%d) unexpected err %v", n, err)
		}
		s := r.Snapshot()
		if s.Prod != 0 || s.Dev != 0 || s.Reap != 0 || s.Free != n {
			t.Fatalf("New(%d) bad initial state %+v", n, s)
		}
		t.Logf("  IN/OUT New(%d) -> ok; 判断依据: 三指针皆 0、Free=N=%d", n, n)
	}
}

func TestSubmitExactlyFreeAndOneMore(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "填满 4 槽", r, m, []int64{1, 2, 3, 4})
		if f := r.Free(); f != 0 {
			t.Fatalf("free=%d want 0", f)
		}
		t.Log("  IN/OUT Free() -> 0; 判断依据: prod-reap=4=N，可占满不留空槽")
		diffSubmit(t, "比 Free 多一", r, m, []int64{5})
		t.Log("  判断依据: m=1 > Free=0 报 ErrNoFreeSlots，且槽/指针不变")
	})
}

func TestSubmitEqualAndGreaterThanN(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		lens := make([]int64, 5)
		for i := range lens {
			lens[i] = 1
		}
		diffSubmit(t, "段数大于 N", r, m, lens)
		t.Log("  判断依据: 5>N=4 报 ErrSegmentsOverN，先于 Free 判定")
		diffSubmit(t, "段数等于 N", r, m, []int64{2, 2, 2, 2})
		diffRun(t, "设备全部处理", r, m, 4)
		diffReap(t, "整包回收", r, m)
	})
}

func TestSingleSegmentFirstAndLast(t *testing.T) {
	runModels(t, 2, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "m=1 单段包", r, m, []int64{7})
		s := r.Snapshot().Slots[0]
		if s.FIRST != 1 || s.LAST != 1 {
			t.Fatalf("FIRST=%d LAST=%d want both 1", s.FIRST, s.LAST)
		}
		t.Logf("  IN/OUT slot[0] FIRST=%d LAST=%d; 判断依据: m=1 时同一槽两个标记都为 1", s.FIRST, s.LAST)
	})
}

func TestFreeUnchangedUntilReap(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "提交 2 段", r, m, []int64{3, 4})
		before := r.Free()
		diffRun(t, "设备处理但不回收", r, m, 2)
		if r.Free() != before {
			t.Fatalf("Free changed %d -> %d before Reap", before, r.Free())
		}
		t.Logf("  IN/OUT 设备处理前后 Free 均=%d; 判断依据: Free 只随 Reap 增加，设备已处理未回收的槽仍占用", before)
		diffReap(t, "回收后释放", r, m)
		if r.Free() != 4 {
			t.Fatalf("Free after reap = %d want 4", r.Free())
		}
		t.Log("  IN/OUT Reap 后 Free -> 4; 判断依据: prod-reap 归零")
	})
}

func TestPointersBeyondNWraparound(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		for cycle := 0; cycle < 5; cycle++ {
			tg := fmt.Sprintf("周期%d", cycle)
			diffSubmit(t, tg+" 提交", r, m, []int64{1, 1, 1, 1})
			diffRun(t, tg+" 设备", r, m, 4)
			diffReap(t, tg+" 回收", r, m)
		}
		s := r.Snapshot()
		if s.Prod != 20 || s.Dev != 20 || s.Reap != 20 {
			t.Fatalf("pointers=%d,%d,%d want 20", s.Prod, s.Dev, s.Reap)
		}
		t.Logf("  IN/OUT 5 个周期后 prod=%d 物理槽=prod%%4=%d; 判断依据: 指针只增不回绕，槽号取余复用", s.Prod, s.Prod%uint64(s.N))
	})
}

func TestFaultCascadeAndReapIndex(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "4 段包", r, m, []int64{10, 20, 30, 40})
		diffFault(t, "登记 seq=1", r, m, 1)
		diffRun(t, "先处理首段(1 单位)", r, m, 1)
		diffRun(t, "连带丢弃(1 单位)", r, m, 1)
		s := r.Snapshot()
		wantSt := []int{StDone, StFault, StDrop, StDrop}
		for i, st := range wantSt {
			if s.Slots[i].St != st || s.Slots[i].OWN != 0 {
				t.Fatalf("slot %d st=%d own=%d want st=%d own=0", i, s.Slots[i].St, s.Slots[i].OWN, st)
			}
		}
		t.Logf("  IN/OUT 各槽 st=%v; 判断依据: 出错段 st=2，同包其后至 LAST（含）st=3，全部 OWN=0", wantSt)
		if s.Dev != 4 {
			t.Fatalf("dev=%d want 4: cascade 越过整个包", s.Dev)
		}
		diffReap(t, "回收出错包", r, m)
		t.Log("  判断依据: Reap 返回段数 4、总长 100、出错段包内下标 FaultIndex=1")
	})
}

func TestFaultOnDiscardedSegmentVoided(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "3 段包", r, m, []int64{5, 5, 5})
		diffFault(t, "登记 seq=0", r, m, 0)
		// seq=2 是 LAST，将随连带丢弃被置 st=3；其上的故障登记必须作废。
		diffFault(t, "登记 seq=2(将被丢弃)", r, m, 2)
		diffRun(t, "连带丢弃整包", r, m, 1)
		if st := r.Snapshot().Slots[2].St; st != StDrop {
			t.Fatalf("slot2 st=%d want st=3: 落在连带丢弃段上的 Fault 须作废", st)
		}
		t.Logf("  IN/OUT slot[2].St=%d; 判断依据: Fault 落在被连带丢弃段上作废，该段仍 st=3", StDrop)
		diffReap(t, "回收丢弃包", r, m)
		diffSubmit(t, "再提交 1 段", r, m, []int64{9})
		diffRun(t, "处理新段不得误出错", r, m, 1)
		if st := r.Snapshot().Slots[3].St; st != StDone {
			t.Fatalf("new segment st=%d want st=1: 作废的故障登记泄漏", st)
		}
		t.Logf("  IN/OUT 新槽 st=%d; 判断依据: 旧登记已随丢弃作废，不影响后续同物理槽的描述符", StDone)
	})
}

func TestCascadeCostsOneUnit(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "3 段包", r, m, []int64{1, 1, 1})
		diffFault(t, "登记首段出错", r, m, 0)
		diffRun(t, "k=1 只给 1 个名额", r, m, 1)
		if r.Dev() != 3 {
			t.Fatalf("dev=%d want 3: 整个连带只占一个单位却越过 3 个描述符", r.Dev())
		}
		t.Logf("  IN/OUT DeviceRun(1) 后 dev=%d; 判断依据: 出错连带 3 段只算 1 个处理单位", r.Dev())
	})
}

func TestCascadeThenMoreInSameRun(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "2 段包", r, m, []int64{1, 1})
		diffSubmit(t, "再 1 段包", r, m, []int64{7})
		diffFault(t, "登记 seq=0 出错", r, m, 0)
		// k=2: unit 1 is the 2-descriptor cascade, unit 2 processes the
		// next packet's descriptor normally.
		diffRun(t, "k=2: 连带+正常各一单位", r, m, 2)
		if r.Dev() != 3 {
			t.Fatalf("dev=%d want 3", r.Dev())
		}
		if st := r.Snapshot().Slots[2].St; st != StDone {
			t.Fatalf("slot2 st=%d want st=1", st)
		}
		t.Logf("  IN/OUT DeviceRun(2) units=2 dev=%d; 判断依据: 连带占 1 名额，同次运行内后续正常描述符占第 2 名额", r.Dev())
	})
}

func TestReapValidationOrder(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffReap(t, "无包先报 ErrNoPacket", r, m)
		diffSubmit(t, "提交 2 段", r, m, []int64{1, 2})
		diffReap(t, "有包未完成报 Incomplete", r, m)
		t.Log("  判断依据: Reap 先判 reap==prod(无包)，再判包内存在 OWN=1(未完成)")
	})
}

func TestSubmitValidationOrder(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffSubmit(t, "空表", r, m, nil)
		diffSubmit(t, "长度非法", r, m, []int64{1, 0, -2})
		diffSubmit(t, "超 N 且含非法长度", r, m, []int64{1, 0, 1, 1, 1})
		diffSubmit(t, "超 N", r, m, []int64{1, 1, 1, 1, 1})
		diffSubmit(t, "占满 4", r, m, []int64{1, 1, 1, 1})
		diffSubmit(t, "Free 不足", r, m, []int64{1})
		t.Log("  判断依据: 顺序为 空表→长度非法→>N→>Free，只报第一个；被拒操作不改状态")
		if r.Prod() != 4 {
			t.Fatalf("prod=%d want 4", r.Prod())
		}
	})
}

func TestDeviceRunStopsAtOwnedByHost(t *testing.T) {
	runModels(t, 4, func(t *testing.T, r *Ring, m *naiveRing) {
		diffRun(t, "空环 k=3 完成 0", r, m, 3)
		diffRun(t, "k=0 返回 0", r, m, 0)
		diffRun(t, "k 为负", r, m, -1)
		diffSubmit(t, "2 段包", r, m, []int64{4, 5})
		diffRun(t, "k 很大但遇 OWN=0 停", r, m, 10)
	})
}

// Randomized differential test against the stepwise naive model.
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			n := []int{2, 4, 8}[rng.Intn(3)]
			r, _ := New(n)
			m := newNaive(n)
			var logBuf []string
			for step := 0; step < 600; step++ {
				switch rng.Intn(5) {
				case 0, 1: // submit
					mSeg := rng.Intn(n + 2)
					lens := make([]int64, mSeg)
					for i := range lens {
						if rng.Intn(10) == 0 {
							lens[i] = int64(rng.Intn(2)) // sometimes 0 (bad)
						} else {
							lens[i] = int64(1 + rng.Intn(99))
						}
					}
					errR, errM := r.Submit(lens), m.naiveSubmit(lens)
					logBuf = append(logBuf, fmt.Sprintf("Submit(%v)->%v|%v", lens, errR, errM))
					if errR != errM {
						t.Fatalf("seed=%d step=%d submit err %v vs %v\n%s", seed, step, errR, errM, strings.Join(logBuf, "\n"))
					}
				case 2: // device run
					k := rng.Intn(n + 3)
					uR, eR := r.DeviceRun(k)
					uM, eM := m.naiveDeviceRun(k)
					logBuf = append(logBuf, fmt.Sprintf("Run(%d)->(%d,%v)|(%d,%v)", k, uR, eR, uM, eM))
					if uR != uM || eR != eM {
						t.Fatalf("seed=%d step=%d run mismatch\n%s", seed, step, strings.Join(logBuf, "\n"))
					}
				case 3: // fault, sometimes at a near-future sequence
					var seq uint64
					switch rng.Intn(3) {
					case 0:
						if m.dev > 0 {
							seq = m.dev - 1 // invalid
						} else {
							seq = m.dev
						}
					case 1:
						seq = m.dev
					default:
						seq = m.dev + uint64(rng.Intn(n+3))
					}
					eR, eM := r.Fault(seq), m.naiveFault(seq)
					logBuf = append(logBuf, fmt.Sprintf("Fault(%d)->%v|%v", seq, eR, eM))
					if eR != eM {
						t.Fatalf("seed=%d step=%d fault err %v vs %v\n%s", seed, step, eR, eM, strings.Join(logBuf, "\n"))
					}
				case 4: // reap
					resR, eR := r.Reap()
					resM, eM := m.naiveReap()
					logBuf = append(logBuf, fmt.Sprintf("Reap()->(%+v,%v)|(%+v,%v)", resR, eR, resM, eM))
					if eR != eM || resR != resM {
						t.Fatalf("seed=%d step=%d reap mismatch\n%s", seed, step, strings.Join(logBuf, "\n"))
					}
				}
				snap := r.Snapshot()
				if snap.Prod != m.prod || snap.Dev != m.dev || snap.Reap != m.reap ||
					snap.Free != m.free() || !slotsEqual(snap.Slots, m.slots) {
					t.Fatalf("seed=%d step=%d state mismatch\nreal  %+v\nnaive %+v\n%s",
						seed, step, snap, m, strings.Join(logBuf, "\n"))
				}
				checkInvariants(t, "fuzz", snap)
			}
			t.Logf("  IN seed=%d N=%d 600 步随机操作 -> OUT 每步与朴素模拟一致；判定依据: 返回值、错误、槽内容、三指针全部相等。尾部日志:\n    %s",
				seed, n, strings.Join(logBuf[len(logBuf)-6:], "\n    "))
		})
	}
}

// Determinism: replaying the identical recorded sequence twice yields
// identical snapshots, results and errors.
type recordedOp struct {
	kind string
	lens []int64
	k    int
	seq  uint64
}

func replayOps(ops []recordedOp) ([]string, Snapshot) {
	r, _ := New(4)
	var out []string
	for _, op := range ops {
		switch op.kind {
		case "submit":
			out = append(out, fmt.Sprintf("Submit(%v)->%v", op.lens, r.Submit(op.lens)))
		case "run":
			u, e := r.DeviceRun(op.k)
			out = append(out, fmt.Sprintf("Run(%d)->(%d,%v)", op.k, u, e))
		case "fault":
			out = append(out, fmt.Sprintf("Fault(%d)->%v", op.seq, r.Fault(op.seq)))
		case "reap":
			res, e := r.Reap()
			out = append(out, fmt.Sprintf("Reap()->(%+v,%v)", res, e))
		}
	}
	return out, r.Snapshot()
}

func TestDeterministicReplay(t *testing.T) {
	ops := []recordedOp{
		{kind: "submit", lens: []int64{3, 6, 9}},
		{kind: "fault", seq: 1},
		{kind: "fault", seq: 2},
		{kind: "run", k: 1},
		{kind: "reap"},
		{kind: "submit", lens: []int64{1, 1, 1, 1}},
		{kind: "run", k: 4},
		{kind: "reap"},
	}
	out1, snap1 := replayOps(ops)
	out2, snap2 := replayOps(ops)
	if strings.Join(out1, "\n") != strings.Join(out2, "\n") {
		t.Fatalf("replay outputs differ:\n%s\nvs\n%s", out1, out2)
	}
	if fmt.Sprintf("%+v", snap1.Slots) != fmt.Sprintf("%+v", snap2.Slots) || snap1.Prod != snap2.Prod ||
		snap1.Dev != snap2.Dev || snap1.Reap != snap2.Reap {
		t.Fatalf("replay snapshots differ")
	}
	t.Logf("  IN 同一操作序列重放两次 -> OUT 输出与快照完全相同；判定依据:\n    %s", strings.Join(out1, "\n    "))
}

func TestConcurrent(t *testing.T) {
	r, _ := New(8)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	legalErr := func(e error) bool {
		switch e {
		case nil, ErrEmptyLens, ErrBadLen, ErrSegmentsOverN, ErrNoFreeSlots,
			ErrNegativeK, ErrFaultBeforeDev, ErrNoPacket, ErrPacketIncomplete:
			return true
		}
		return false
	}

	worker := func(name string, fn func() error) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := fn(); !legalErr(err) {
					t.Errorf("%s unexpected error %v", name, err)
					return
				}
				checkInvariants(t, name, r.Snapshot())
			}
		}
	}

	wg.Add(5)
	go worker("submitter", func() error { return r.Submit([]int64{1, 2, 3}) })
	go worker("device", func() error {
		_, e := r.DeviceRun(2)
		return e
	})
	go worker("fault", func() error { return r.Fault(r.Dev()) })
	go worker("reaper", func() error {
		_, e := r.Reap()
		return e
	})
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				checkInvariants(t, "reader", r.Snapshot())
			}
		}
	}()

	for i := 0; i < 100000; i++ {
		r.Snapshot()
	}
	close(stop)
	wg.Wait()
	checkInvariants(t, "concurrent-final", r.Snapshot())
	t.Logf("  IN 5 个 goroutine 并发 submit/run/fault/reap/snapshot -> OUT 全程不变量成立；最终 prod=%d dev=%d reap=%d free=%d",
		r.Prod(), r.Dev(), r.ReapPtr(), r.Free())
}
