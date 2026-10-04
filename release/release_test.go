package release_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/budget"
	"ontology/probe"
	"ontology/release"
)

func newSys(hi, delta, w, g, h, bmax, q int64) (*release.System, *probe.Store, *budget.Tracker) {
	p := probe.New(hi, delta, w, g)
	tr := budget.New(p, h, bmax)
	return release.New(p, tr, q), p, tr
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

const (
	opR  = release.PermRelease
	opQ  = release.PermQA
	opRQ = opR | opQ
)

// 题面主例：Hi=80 Δ=20 w=3 G=10 H=5 Bmax=30 Q=50。
func TestSpecExample(t *testing.T) {
	setup := func() *release.System {
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 0))
		return sys
	}
	sys := setup()
	must(t, sys.Register("U", "D", 5))
	must(t, sys.Reading("D", 90, 10))
	must(t, sys.Reading("D", 70, 20))
	check := func(now, wantE, wantSpoil int64) {
		t.Helper()
		r, err := sys.Evaluate("U", now)
		must(t, err)
		if r.E != wantE || r.SpoiledAt != wantSpoil {
			t.Fatalf("Evaluate(%d): E=%d spoil=%d want E=%d spoil=%d",
				now, r.E, r.SpoiledAt, wantE, wantSpoil)
		}
		t.Logf("输入 Evaluate(U,%d) 输出 {E:%d 判废:%v at:%d} 依据: 阶梯+缺口逐分钟",
			now, r.E, r.Spoiled, r.SpoiledAt)
	}
	check(20, 10, 0)
	check(30, 10, 0)
	check(36, 28, 0)
	check(37, 31, 37)
	must(t, sys.Reading("D", 60, 45))

	// Release t=25 不需 QA 且冻结；冻结后 Load/Unload/Release 均状态不符。
	{
		s2 := setup()
		must(t, s2.Register("U", "D", 5))
		must(t, s2.Reading("D", 90, 10)) // [10,20) 轻度；t=25 还含缺口 5 分钟
		must(t, s2.Reading("D", 70, 20))
		// [5,10) 正常 + [10,20) 轻度 10 + [20,25) 正常 → E(25)=10，不需 QA。
		must(t, s2.Release("U", release.Operator{Name: "a", Perm: opR}, 25))
		wantErr(t, s2.Load("U", "D", 30), probe.ErrState)
		wantErr(t, s2.Unload("U", "D", 30), probe.ErrState)
		wantErr(t, s2.Release("U", release.Operator{Name: "a", Perm: opRQ}, 30), probe.ErrState)
		r, err := s2.Evaluate("U", 100)
		must(t, err)
		if r.E != 10 {
			t.Fatalf("frozen E=%d want 10", r.E)
		}
	}
	// t=32 E=16, 1600>=1500 需 QA；无 QA 拒绝且有 QA 成功。
	{
		s3 := setup()
		must(t, s3.Register("U", "D", 5))
		must(t, s3.Reading("D", 90, 10))
		must(t, s3.Reading("D", 70, 20))
		wantErr(t, s3.Release("U", release.Operator{Name: "a", Perm: opR}, 32), probe.ErrQA)
		must(t, s3.Release("U", release.Operator{Name: "a", Perm: opRQ}, 32))
	}
	// t=37 起报已判废。
	{
		s4 := setup()
		must(t, s4.Register("U", "D", 5))
		must(t, s4.Reading("D", 90, 10))
		must(t, s4.Reading("D", 70, 20))
		wantErr(t, s4.Release("U", release.Operator{Name: "a", Perm: opRQ}, 37), probe.ErrSpoiled)
	}
}

// 恰等 Bmax 不判废、Bmax=31 时 SpoiledAt=38。
func TestEqualityBoundary(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 10, 5, 31, 50)
	must(t, sys.Reading("D", 50, 0))
	must(t, sys.Register("U", "D", 5))
	must(t, sys.Reading("D", 90, 10))
	must(t, sys.Reading("D", 70, 20))
	r37, err := sys.Evaluate("U", 37)
	must(t, err)
	if r37.E != 31 || r37.Spoiled {
		t.Fatalf("at 37 E=%d spoiled=%v, want 31/false", r37.E, r37.Spoiled)
	}
	r38, err := sys.Evaluate("U", 38)
	must(t, err)
	if r38.E != 34 || r38.SpoiledAt != 38 {
		t.Fatalf("at 38 E=%d spoil=%d, want 34/38", r38.E, r38.SpoiledAt)
	}
	t.Logf("输入 Bmax=31: t=37 E=31 恰等不判废；t=38 E=34 SpoiledAt=38")
}

// 温度恰等 Hi 与 Hi+Δ 的分档。
func TestTempBoundaries(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 1000, 5, 10000, 1)
	must(t, sys.Reading("D", 80, 0)) // 恰等 Hi → 正常
	must(t, sys.Register("U", "D", 0))
	must(t, sys.Reading("D", 100, 10)) // 恰等 Hi+Δ → 轻度
	must(t, sys.Reading("D", 101, 20)) // 重度
	r, err := sys.Evaluate("U", 30)
	must(t, err)
	if r.E != 40 { // 0 + 10 + 30
		t.Fatalf("E=%d want 40", r.E)
	}
}

// 间隔恰等 G 无缺口；比 G 大 1 多出 1 分钟缺口；末条读数后的缺口。
func TestGapBoundaries(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 10, 5, 100000, 1)
	must(t, sys.Reading("D", 50, 0))
	must(t, sys.Register("U", "D", 0))
	must(t, sys.Reading("D", 50, 10)) // 恰等 G
	must(t, sys.Reading("D", 50, 21)) // 大 1：[20,21) 缺口
	r, err := sys.Evaluate("U", 21)
	must(t, err)
	if r.E != 3 {
		t.Fatalf("E(21)=%d want 3", r.E)
	}
	r2, err := sys.Evaluate("U", 40) // 末条覆盖 [21,31)，[31,40) 缺口
	must(t, err)
	if r2.E != 30 {
		t.Fatalf("E(40)=%d want 30", r2.E)
	}
}

// 空档：8 分钟宽限切分、恰等 H、未结束空档。
func TestGapGrace(t *testing.T) {
	build := func() *release.System {
		sys, _, _ := newSys(80, 20, 3, 1000, 5, 100000, 1)
		must(t, sys.Reading("D2", 50, 0))
		return sys
	}
	{
		sys := build()
		must(t, sys.Register("V", "D2", 0))
		must(t, sys.Unload("V", "D2", 2))
		must(t, sys.Reading("D2", 50, 5))
		must(t, sys.Reading("D2", 50, 10))
		must(t, sys.Load("V", "D2", 10))
		r, err := sys.Evaluate("V", 10)
		must(t, err)
		if r.E != 14 { // 5 + 3*3
			t.Fatalf("E(10)=%d want 14", r.E)
		}
	}
	{
		sys := build()
		must(t, sys.Register("V", "D2", 0))
		must(t, sys.Unload("V", "D2", 2))
		must(t, sys.Load("V", "D2", 7)) // 空档恰等 H
		r, err := sys.Evaluate("V", 7)
		must(t, err)
		if r.E != 5 {
			t.Fatalf("E(7)=%d want 5", r.E)
		}
	}
	{
		sys := build()
		must(t, sys.Register("V", "D2", 0))
		must(t, sys.Unload("V", "D2", 2))
		r6, err := sys.Evaluate("V", 6)
		must(t, err)
		if r6.E != 4 {
			t.Fatalf("E(6)=%d want 4", r6.E)
		}
		r8, err := sys.Evaluate("V", 8)
		must(t, err)
		if r8.E != 8 {
			t.Fatalf("E(8)=%d want 8", r8.E)
		}
	}
}

// 多段装载跨设备。
func TestMultiDeviceSegments(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 1000, 5, 100000, 1)
	must(t, sys.Reading("A", 90, 0))
	must(t, sys.Reading("B", 101, 0))
	must(t, sys.Register("X", "A", 0))
	must(t, sys.Unload("X", "A", 10)) // 10 轻度
	must(t, sys.Load("X", "B", 15))   // 空档 5 轻度
	must(t, sys.Unload("X", "B", 20)) // 5 重度 = 15
	r, err := sys.Evaluate("X", 20)
	must(t, err)
	if r.E != 30 {
		t.Fatalf("E=%d want 30", r.E)
	}
}

// 复核比例取等：E*100 == Bmax*Q 仍需 QA。
func TestQAEquality(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 1000, 5, 20, 50)
	must(t, sys.Reading("D", 90, 0))
	must(t, sys.Register("U", "D", 0))
	wantErr(t, sys.Release("U", release.Operator{Name: "o", Perm: opR}, 10), probe.ErrQA)
	must(t, sys.Release("U", release.Operator{Name: "o", Perm: opRQ}, 10))
}

// 拒绝次序与各类哨兵错误。
func TestRejectionOrder(t *testing.T) {
	// 参数非法 > 时钟回退：空标识配合回退时刻仍报非法。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 10))
		wantErr(t, sys.Reading("", 50, 5), probe.ErrInvalid)
		wantErr(t, sys.Reading("D", 1<<30, 5), probe.ErrInvalid)
		wantErr(t, sys.Register("", "D", 5), probe.ErrInvalid)
		wantErr(t, sys.Reading("D", 50, 9), probe.ErrClock)
	}
	// 读数时刻相等：状态不符；被拒绝不推进时钟（后续 11 可接受）。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 10))
		wantErr(t, sys.Reading("D", 60, 10), probe.ErrState)
		must(t, sys.Reading("D", 60, 11))
	}
	// 时钟回退 > 无 Release 权限：无权限且回退时刻，先报回退。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 10))
		wantErr(t, sys.Release("Z", release.Operator{Name: "o", Perm: 0}, 5), probe.ErrClock)
	}
	// 无 Release 权限 > 不存在。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		wantErr(t, sys.Release("Z", release.Operator{Name: "o", Perm: 0}, 10), probe.ErrForbidden)
	}
	// 不存在 > 状态不符：Register 到无读数设备在“无该单元”之后仍属状态。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		wantErr(t, sys.Register("U", "NODEV", 10), probe.ErrState)
	}
	// Register 已登记 → 冲突（ErrConflict，errors.Is 可辨）。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 0))
		must(t, sys.Register("U", "D", 0))
		wantErr(t, sys.Register("U", "D", 1), probe.ErrConflict)
	}
	// Load 已在设备 / Unload 不在设备。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 30, 50)
		must(t, sys.Reading("D", 50, 0))
		must(t, sys.Register("U", "D", 0))
		wantErr(t, sys.Load("U", "D", 1), probe.ErrState)
		must(t, sys.Unload("U", "D", 2))
		wantErr(t, sys.Unload("U", "D", 3), probe.ErrState)
		wantErr(t, sys.Load("U", "NODEV", 4), probe.ErrState)
	}
	// 已判废 > 缺 QA：判废后即便无 QA 也只报判废；判废后仍可装卸。
	{
		sys, _, _ := newSys(80, 20, 3, 10, 5, 3, 1)
		must(t, sys.Reading("D", 101, 0)) // 重度 w=3
		must(t, sys.Register("U", "D", 0))
		_, err := sys.Evaluate("U", 2)
		must(t, err)
		wantErr(t, sys.Release("U", release.Operator{Name: "o", Perm: opR}, 2), probe.ErrSpoiled)
		must(t, sys.Unload("U", "D", 3))
		must(t, sys.Load("U", "D", 4))
	}
}

// 并发：相同序列并发重放结果等价于某串行顺序（不 panic、不数据竞争）。
func TestConcurrent(t *testing.T) {
	sys, _, _ := newSys(80, 20, 3, 10, 5, 1000000, 50)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dev := "D"
			base := int64(g * 1000)
			for i := int64(0); i < 50; i++ {
				now := base + i*2
				_ = sys.Reading(dev, 90, now)
			}
		}(g)
	}
	wg.Wait()
}

// ---- 朴素逐分钟模拟器（预言机） ----

type naiveState struct {
	hi, delta, w, g, h, bmax, q int64
	readings                    map[string][]rd
	units                       map[string]*nUnit
	nowMax                      int64
}

type rd struct {
	t, temp int64
}

type nUnit struct {
	regAt    int64
	on       bool
	device   string
	events   []ev // 左闭右开区间事件点
	released bool
	frozenE  int64
	frozenAt int64
}

type ev struct {
	t  int64
	on bool
	d  string
}

func newNaive(c cfg) *naiveState {
	return &naiveState{
		hi: c.hi, delta: c.delta, w: c.w, g: c.g, h: c.h, bmax: c.bmax, q: c.q,
		readings: map[string][]rd{}, units: map[string]*nUnit{},
	}
}

type cfg struct{ hi, delta, w, g, h, bmax, q int64 }

// deviceWeight 返回设备 dev 在分钟 m（[m,m+1)）的权重。
func (n *naiveState) deviceWeight(dev string, m int64) (int64, bool) {
	rs := n.readings[dev]
	i := -1
	for j, r := range rs {
		if r.t <= m {
			i = j
		}
	}
	if i < 0 {
		return 0, false
	}
	cur := rs[i]
	if m >= cur.t+n.g {
		return n.w, true // 缺口重度
	}
	switch {
	case cur.temp <= n.hi:
		return 0, true
	case cur.temp <= n.hi+n.delta:
		return 1, true
	default:
		return n.w, true
	}
}

// exposure 逐分钟累加 E(unit, x)。
func (n *naiveState) exposure(u *nUnit, x int64) int64 {
	if u.released && x >= u.frozenAt {
		return u.frozenE
	}
	var sum int64
	for m := u.regAt; m < x; m++ {
		sum += n.minuteWeight(u, m)
	}
	return sum
}

func (n *naiveState) minuteWeight(u *nUnit, m int64) int64 {
	// 确定分钟 m 所在区间：在事件序列里找最后一个 t<=m。
	on := true
	dev := ""
	var gapStart int64 = u.regAt
	for _, e := range u.events {
		if e.t <= m {
			on, dev = e.on, e.d
			if !e.on {
				gapStart = e.t
			}
		}
	}
	if on {
		v, ok := n.deviceWeight(dev, m)
		if !ok {
			v = 0
		}
		return v
	}
	off := m - gapStart
	if off < n.h {
		return 1
	}
	return n.w
}

func (n *naiveState) spoiledAt(u *nUnit) int64 {
	return n.spoiledBy(u, n.nowMax)
}

func (n *naiveState) spoiledBy(u *nUnit, x int64) int64 {
	for k := u.regAt + 1; k <= x; k++ {
		if n.exposureFrozen(u, k) > n.bmax {
			return k
		}
	}
	return 0
}

func (n *naiveState) exposureFrozen(u *nUnit, x int64) int64 {
	var sum int64
	for m := u.regAt; m < x; m++ {
		sum += n.minuteWeight(u, m)
	}
	return sum
}

// TestRandomDifferential 1500 组随机操作序列，与逐分钟朴素模拟逐值对照。
func TestRandomDifferential(t *testing.T) {
	const N = 1500
	rng := rand.New(rand.NewSource(20261004))
	for iter := 0; iter < N; iter++ {
		c := cfg{
			hi:    80,
			delta: int64(1 + rng.Intn(30)),
			w:     int64(2 + rng.Intn(9)),
			g:     int64(2 + rng.Intn(12)),
			h:     int64(1 + rng.Intn(8)),
			bmax:  int64(1 + rng.Intn(120)),
			q:     int64(1 + rng.Intn(100)),
		}
		sys, _, _ := newSys(c.hi, c.delta, c.w, c.g, c.h, c.bmax, c.q)
		nv := newNaive(c)

		const horizon = 120
		devs := []string{"D", "E"}
		// 仅在 t=0 为每台设备放首条读数（满足 Register 前置）；
		// 后续读数在随机循环中按单调时刻插入，与单元操作交错。
		nextReading := map[string]int64{}
		for di, d := range devs {
			temp := []int64{50, 80, 90, 100, 101, 110}[rng.Intn(6)]
			t0 := int64(di) // 严格递增，避免 t=0 回退
			must(t, sys.Reading(d, temp, t0))
			nv.readings[d] = append(nv.readings[d], rd{t: t0, temp: temp})
			nextReading[d] = t0 + int64(1+rng.Intn(int(c.g)+4))
		}

		nUnits := 2 + rng.Intn(2)
		live := map[string]bool{}
		onDev := map[string]string{}
		released := map[string]bool{}
		now := int64(0)
		for step := 0; step < 120; step++ {
			now++
			// 以一定概率为某台设备追加一条严格递增的读数。
			if rng.Intn(3) == 0 {
				d := devs[rng.Intn(len(devs))]
				if nt := nextReading[d]; nt <= now {
					temp := []int64{50, 80, 90, 100, 101, 110}[rng.Intn(6)]
					if err := sys.Reading(d, temp, now); err == nil {
						nv.readings[d] = append(nv.readings[d], rd{t: now, temp: temp})
						nextReading[d] = now + int64(1+rng.Intn(int(c.g)+4))
					}
				}
			}
			uid := fmt.Sprintf("U%d", rng.Intn(nUnits))
			op := rng.Intn(100)
			switch {
			case !live[uid] && op < 60:
				d := devs[rng.Intn(len(devs))]
				err := sys.Register(uid, d, now)
				if err == nil {
					live[uid] = true
					onDev[uid] = d
					nv.units[uid] = &nUnit{regAt: now, on: true, device: d,
						events: []ev{{t: now, on: true, d: d}}}
				}
			case live[uid] && !released[uid] && onDev[uid] != "" && op < 75:
				d := onDev[uid]
				must(t, sys.Unload(uid, d, now))
				onDev[uid] = ""
				u := nv.units[uid]
				u.on = false
				u.device = ""
				u.events = append(u.events, ev{t: now, on: false})
			case live[uid] && !released[uid] && onDev[uid] == "" && op < 90:
				d := devs[rng.Intn(len(devs))]
				must(t, sys.Load(uid, d, now))
				onDev[uid] = d
				u := nv.units[uid]
				u.on = true
				u.device = d
				u.events = append(u.events, ev{t: now, on: true, d: d})
			case live[uid] && !released[uid] && op < 97:
				perm := []release.Perm{opR, opRQ}[rng.Intn(2)]
				err := sys.Release(uid, release.Operator{Name: "op", Perm: perm}, now)
				u := nv.units[uid]
				e := nv.exposure(u, now)
				spoil := func() bool {
					for x := u.regAt + 1; x <= now; x++ {
						if nv.exposureFrozen(u, x) > c.bmax {
							return true
						}
					}
					return false
				}()
				needQA := e*100 >= c.bmax*c.q
				if spoil {
					wantErr(t, err, probe.ErrSpoiled)
				} else if needQA && perm != opRQ {
					wantErr(t, err, probe.ErrQA)
				} else {
					must(t, err)
					released[uid] = true
					u.released = true
					u.frozenE = e
					u.frozenAt = now
					u.events = append(u.events, ev{t: now, on: false})
					onDev[uid] = ""
				}
			}
			nv.nowMax = now

			// 对所有活跃单元，将系统 Evaluate 与朴素 exposure/spoiledAt 对照。
			for uid := range live {
				u := nv.units[uid]
				if u == nil {
					t.Fatalf("live but no naive unit: %s", uid)
				}
				r, err := sys.Evaluate(uid, now)
				must(t, err)
				want := nv.exposure(u, now)
				if r.E != want {
					t.Fatalf("[iter %d] uid=%s now=%d E=%d naive=%d cfg=%+v events=%+v rdD=%v rdE=%v",
						iter, uid, now, r.E, want, c, u.events, nv.readings["D"], nv.readings["E"])
				}
				var sAt int64
				if released[uid] {
					if u.frozenE > c.bmax {
						for x := u.regAt + 1; x <= u.frozenAt; x++ {
							if nv.exposureFrozen(u, x) > c.bmax {
								sAt = x
								break
							}
						}
					}
				} else {
					sAt = nv.spoiledAt(u)
				}
				if (sAt != 0) != r.Spoiled || r.SpoiledAt != sAt {
					var detail []string
					for m := u.regAt; m <= now; m++ {
						detail = append(detail, fmt.Sprintf("%d:%d", m, nv.minuteWeight(u, m)))
					}
					t.Fatalf("[iter %d] uid=%s now=%d spoiled sys=%d naive=%d E=%d cfg=%+v events=%+v w=[%v] rdD=%v rdE=%v",
						iter, uid, now, r.SpoiledAt, sAt, r.E, c, u.events, detail, nv.readings["D"], nv.readings["E"])
				}
			}
		}
		if iter < 5 {
			t.Logf("[iter %d] 输入: cfg=%+v 随机120步 输出: 全部单元 E/SpoiledAt 与朴素模拟一致", iter, c)
		}
	}
}
