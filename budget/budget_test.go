package budget_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/budget"
	"ontology/probe"
	"ontology/release"
)

type system struct {
	probe   *probe.Store
	release *release.Store
	eval    *budget.Evaluator
}

func newSystem(t *testing.T, pc probe.Config, bc budget.Config) *system {
	t.Helper()
	clock := &probe.Clock{}
	ps, err := probe.NewStore(clock, pc)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := release.NewStore(clock, ps)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := budget.NewEvaluator(clock, ps, rs, bc)
	if err != nil {
		t.Fatal(err)
	}
	return &system{probe: ps, release: rs, eval: ev}
}

// 规范示例参数：Hi=80, Delta=20, W=3, G=10, H=5, Bmax=30, Q=50。
func specSystem(t *testing.T, bmax int64) *system {
	t.Helper()
	return newSystem(t,
		probe.Config{Hi: 80, Delta: 20, W: 3, G: 10},
		budget.Config{H: 5, Bmax: bmax, Q: 50})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func checkEval(t *testing.T, s *system, unit string, now, e int64, spoiled bool, at int64, hasAt bool) {
	t.Helper()
	res, err := s.eval.Evaluate(unit, now)
	if err != nil {
		t.Fatalf("Evaluate(%s,%d): %v", unit, now, err)
	}
	want := budget.Result{E: e, Spoiled: spoiled, SpoiledAt: at, HasSpoiledAt: hasAt}
	if res != want {
		t.Fatalf("Evaluate(%s,%d) = %+v, want %+v", unit, now, res, want)
	}
}

// 规范例一：D 读数 t=0:50, t=10:90, t=20:70, t=45:60；U 于 t=5 登记在 D。
func TestSpecExample1(t *testing.T) {
	s := specSystem(t, 30)
	must(t, s.probe.Reading("D", 50, 0))
	must(t, s.release.Register("U", "D", 5))
	must(t, s.probe.Reading("D", 90, 10))
	must(t, s.probe.Reading("D", 70, 20))
	checkEval(t, s, "U", 20, 10, false, 0, false) // [10,20) 轻度 10 分钟
	checkEval(t, s, "U", 36, 28, false, 0, false) // 10 + 3*6
	checkEval(t, s, "U", 37, 31, true, 37, true)  // 10 + 3*7，越过 Bmax=30
	must(t, s.probe.Reading("D", 60, 45))
	checkEval(t, s, "U", 45, 55, true, 37, true) // 缺口持续到 t=45
}

// 规范例一变体：Bmax=31 时 E(U,37)=31 恰等不判废，SpoiledAt=38。
func TestSpecExample1Budget31(t *testing.T) {
	s := specSystem(t, 31)
	must(t, s.probe.Reading("D", 50, 0))
	must(t, s.release.Register("U", "D", 5))
	must(t, s.probe.Reading("D", 90, 10))
	must(t, s.probe.Reading("D", 70, 20))
	checkEval(t, s, "U", 37, 31, false, 0, false)
	checkEval(t, s, "U", 38, 34, true, 38, true)
}

// 规范例一的放行判定：t=25 不需 QA；t=32 需 QA；t=37 起报已判废。
func TestSpecExample1Release(t *testing.T) {
	setup := func(t *testing.T) *system {
		s := specSystem(t, 30)
		must(t, s.probe.Reading("D", 50, 0))
		must(t, s.release.Register("U", "D", 5))
		must(t, s.probe.Reading("D", 90, 10))
		must(t, s.probe.Reading("D", 70, 20))
		return s
	}
	rel := release.Operator{Name: "op", Perms: release.PermRelease}
	qa := release.Operator{Name: "qa", Perms: release.PermRelease | release.PermQA}

	s1 := setup(t)
	must(t, s1.release.Release("U", rel, 25)) // E=10，1000 < 1500
	checkEval(t, s1, "U", 25, 10, false, 0, false)
	checkEval(t, s1, "U", 40, 10, false, 0, false) // 冻结

	s2 := setup(t)
	if err := s2.release.Release("U", rel, 32); !errors.Is(err, probe.ErrNoQAPerm) {
		t.Fatalf("t=32 without QA: %v", err) // E=16，1600 >= 1500
	}
	must(t, s2.release.Release("U", qa, 32))
	checkEval(t, s2, "U", 40, 16, false, 0, false)

	s3 := setup(t)
	if err := s3.release.Release("U", qa, 37); !errors.Is(err, probe.ErrSpoiled) {
		t.Fatalf("t=37: %v", err) // E=31 > 30 已判废
	}
}

// 规范例二：D2 读数恒为 50 且无缺口；空档前 5 分钟轻度、其余重度。
func TestSpecExample2(t *testing.T) {
	s := specSystem(t, 30)
	must(t, s.probe.Reading("D2", 50, 0))
	must(t, s.release.Register("V", "D2", 0))
	must(t, s.release.Unload("V", "D2", 2))
	checkEval(t, s, "V", 6, 4, false, 0, false) // 空档未结束：4 分钟轻度
	checkEval(t, s, "V", 8, 8, false, 0, false) // 5 轻度 + 3 重度
	must(t, s.release.Load("V", "D2", 10))
	checkEval(t, s, "V", 10, 14, false, 0, false) // 5 + 3*3

	// 空档恰等 H=5：全为轻度。
	s2 := specSystem(t, 30)
	must(t, s2.probe.Reading("D2", 50, 0))
	must(t, s2.release.Register("V", "D2", 0))
	must(t, s2.release.Unload("V", "D2", 2))
	must(t, s2.release.Load("V", "D2", 7))
	checkEval(t, s2, "V", 7, 5, false, 0, false)
}

type step struct {
	kind      string // read / reg / unload / load / release
	dev, unit string
	temp, now int64
	perms     release.Perm
}

type check struct {
	unit    string
	now, e  int64
	spoiled bool
	at      int64
	hasAt   bool
}

// 边界用例表：E 恰等 Bmax、重度一步越过、空档恰等 H、温度恰等 Hi 与 Hi+Δ、
// 间隔恰等 G 与大 1、最后一条读数后的缺口、多段装载跨设备、复核比例取等、放行后冻结。
func TestBoundaries(t *testing.T) {
	defPC := probe.Config{Hi: 80, Delta: 20, W: 3, G: 10}
	defBC := budget.Config{H: 5, Bmax: 30, Q: 50}
	qa := release.PermRelease | release.PermQA
	cases := []struct {
		name  string
		bc    budget.Config
		steps []step
		want  []check
	}{
		{
			name: "E恰等Bmax不判废",
			steps: []step{
				{kind: "read", dev: "D", temp: 200, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
			},
			want: []check{
				{unit: "U", now: 10, e: 30, spoiled: false},
				{unit: "U", now: 11, e: 33, spoiled: true, at: 11, hasAt: true},
			},
		},
		{
			name: "重度区间一步越过预算",
			bc:   budget.Config{H: 5, Bmax: 31, Q: 50},
			steps: []step{
				{kind: "read", dev: "D", temp: 200, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
			},
			want: []check{
				{unit: "U", now: 10, e: 30, spoiled: false},
				// E 从 30 一步到 33，越过 31 与 32，判废时刻取那一分钟。
				{unit: "U", now: 11, e: 33, spoiled: true, at: 11, hasAt: true},
			},
		},
		{
			name: "空档恰等H全为轻度",
			steps: []step{
				{kind: "read", dev: "D", temp: 50, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
				{kind: "unload", unit: "U", dev: "D", now: 2},
				{kind: "load", unit: "U", dev: "D", now: 7},
			},
			want: []check{{unit: "U", now: 7, e: 5, spoiled: false}},
		},
		{
			name: "温度恰等Hi为正常",
			steps: []step{
				{kind: "read", dev: "D", temp: 80, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
			},
			want: []check{{unit: "U", now: 10, e: 0, spoiled: false}},
		},
		{
			name: "温度恰等Hi+Δ为轻度",
			steps: []step{
				{kind: "read", dev: "D", temp: 100, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
			},
			want: []check{{unit: "U", now: 10, e: 10, spoiled: false}},
		},
		{
			name: "间隔恰等G无缺口",
			steps: []step{
				{kind: "read", dev: "D", temp: 90, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
				{kind: "read", dev: "D", temp: 90, now: 10},
			},
			want: []check{{unit: "U", now: 20, e: 20, spoiled: false}},
		},
		{
			name: "间隔大1缺口1分钟",
			steps: []step{
				{kind: "read", dev: "D", temp: 90, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
				{kind: "read", dev: "D", temp: 90, now: 11},
			},
			// [0,10) 轻度 10，[10,11) 缺口重度 3，[11,21) 轻度 10。
			want: []check{{unit: "U", now: 21, e: 23, spoiled: false}},
		},
		{
			name: "最后一条读数之后的缺口",
			steps: []step{
				{kind: "read", dev: "D", temp: 50, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
			},
			// [0,10) 正常，[10,15) 缺口重度 5*3。
			want: []check{{unit: "U", now: 15, e: 15, spoiled: false}},
		},
		{
			name: "多段装载跨设备",
			steps: []step{
				{kind: "read", dev: "D", temp: 90, now: 0},
				{kind: "read", dev: "D2", temp: 200, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
				{kind: "unload", unit: "U", dev: "D", now: 10},
				{kind: "load", unit: "U", dev: "D2", now: 15},
			},
			want: []check{
				{unit: "U", now: 15, e: 15, spoiled: false}, // 10 轻度 + 空档 5
				{unit: "U", now: 20, e: 30, spoiled: false}, // + 5*3 恰等 Bmax
				{unit: "U", now: 21, e: 33, spoiled: true, at: 21, hasAt: true},
			},
		},
		{
			name: "复核比例取等放行后冻结",
			steps: []step{
				{kind: "read", dev: "D", temp: 200, now: 0},
				{kind: "reg", unit: "U", dev: "D", now: 0},
				// E=15，15*100 == 30*50，恰等也需 QA；带 QA 放行成功。
				{kind: "release", unit: "U", now: 5, perms: qa},
			},
			want: []check{
				{unit: "U", now: 5, e: 15, spoiled: false},
				{unit: "U", now: 100, e: 15, spoiled: false}, // 冻结
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bc := tc.bc
			if bc == (budget.Config{}) {
				bc = defBC
			}
			s := newSystem(t, defPC, bc)
			for i, st := range tc.steps {
				var err error
				switch st.kind {
				case "read":
					err = s.probe.Reading(st.dev, st.temp, st.now)
				case "reg":
					err = s.release.Register(st.unit, st.dev, st.now)
				case "unload":
					err = s.release.Unload(st.unit, st.dev, st.now)
				case "load":
					err = s.release.Load(st.unit, st.dev, st.now)
				case "release":
					err = s.release.Release(st.unit, release.Operator{Name: "op", Perms: st.perms}, st.now)
				}
				if err != nil {
					t.Fatalf("step %d (%s): %v", i, st.kind, err)
				}
			}
			for _, c := range tc.want {
				checkEval(t, s, c.unit, c.now, c.e, c.spoiled, c.at, c.hasAt)
			}
		})
	}
}

// Evaluate 的拒绝次序：参数非法 > 时钟回退 > 不存在。
func TestEvaluateRejectionOrder(t *testing.T) {
	s := specSystem(t, 30)
	must(t, s.probe.Reading("D", 50, 10))
	if _, err := s.eval.Evaluate("", 5); !errors.Is(err, probe.ErrInvalidParam) {
		t.Fatalf("empty unit + clock back: %v", err)
	}
	if _, err := s.eval.Evaluate("U", 5); !errors.Is(err, probe.ErrClockBack) {
		t.Fatalf("clock back + unknown unit: %v", err)
	}
	if _, err := s.eval.Evaluate("U", 10); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("unknown unit: %v", err)
	}
	if _, err := s.eval.Evaluate("U", probe.MaxNow+1); !errors.Is(err, probe.ErrInvalidParam) {
		t.Fatalf("now out of range: %v", err)
	}
}

// 并发冒烟：所有操作在共享时钟锁内串行，最终结果与串行重放一致。
func TestConcurrentSmoke(t *testing.T) {
	s := newSystem(t,
		probe.Config{Hi: 80, Delta: 20, W: 3, G: 10},
		budget.Config{H: 5, Bmax: 1_000_000, Q: 50})
	for i := int64(0); i <= 200; i++ {
		must(t, s.probe.Reading("D", 90, i)) // 全部轻度
	}
	must(t, s.release.Register("U", "D", 200))
	var now atomic.Int64
	now.Store(200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				n := now.Add(1)
				_, _ = s.eval.Evaluate("U", n) // 乱序到达者报时钟回退，忽略
			}
		}()
	}
	wg.Wait()
	final := now.Load() + 1
	// [200,210) 轻度，[210,final) 为最后读数后的缺口（重度 3）。
	want := int64(0)
	for tm := int64(200); tm < final; tm++ {
		if tm < 210 {
			want += 1
		} else {
			want += 3
		}
	}
	checkEval(t, s, "U", final, want, false, 0, false)
}

// ---- 朴素模拟：逐分钟累加，作为随机对照基准 ----

type nReading struct{ t, temp int64 }

type nInterval struct {
	dev        string
	start, end int64 // end=-1 表示仍在装载
}

type nUnit struct {
	reg      int64
	ivs      []nInterval
	released bool
	frozen   int64
}

type naive struct {
	hi, delta, w, g, h, bmax, q int64
	maxNow                      int64
	devs                        map[string][]nReading
	units                       map[string]*nUnit
}

func newNaive(hi, delta, w, g, h, bmax, q int64) *naive {
	return &naive{hi: hi, delta: delta, w: w, g: g, h: h, bmax: bmax, q: q,
		devs: make(map[string][]nReading), units: make(map[string]*nUnit)}
}

func (n *naive) advance(now int64) {
	if now > n.maxNow {
		n.maxNow = now
	}
}

func (n *naive) tempWeight(temp int64) int64 {
	if temp <= n.hi {
		return 0
	}
	if temp <= n.hi+n.delta {
		return 1
	}
	return n.w
}

func (n *naive) devWeight(dev string, t int64) int64 {
	rs := n.devs[dev]
	var r *nReading
	for i := range rs {
		if rs[i].t <= t {
			r = &rs[i]
		} else {
			break
		}
	}
	if r == nil {
		return 0
	}
	if t-r.t < n.g {
		return n.tempWeight(r.temp)
	}
	return n.w // 缺口按重度
}

func (n *naive) minuteWeight(u *nUnit, t int64) int64 {
	prev := u.reg
	for _, iv := range u.ivs {
		end := iv.end
		if end < 0 {
			end = math.MaxInt64
		}
		if t < iv.start { // 交接空档
			if t-prev < n.h {
				return 1
			}
			return n.w
		}
		if t < end {
			return n.devWeight(iv.dev, t)
		}
		prev = end
	}
	if t-prev < n.h { // 尚未结束的空档
		return 1
	}
	return n.w
}

func (n *naive) exposure(u *nUnit, x int64) int64 {
	e := int64(0)
	for t := u.reg; t < x; t++ {
		e += n.minuteWeight(u, t)
	}
	return e
}

func (n *naive) spoiledAt(u *nUnit, x int64) (int64, bool) {
	e := int64(0)
	for t := u.reg; t < x; t++ {
		e += n.minuteWeight(u, t)
		if e > n.bmax {
			return t + 1, true
		}
	}
	return 0, false
}

func (n *naive) reading(dev string, temp, now int64) error {
	if dev == "" || temp < probe.MinTemp || temp > probe.MaxTemp || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return probe.ErrClockBack
	}
	rs := n.devs[dev]
	if len(rs) > 0 && now == rs[len(rs)-1].t {
		return probe.ErrState
	}
	n.devs[dev] = append(rs, nReading{now, temp})
	n.advance(now)
	return nil
}

func (n *naive) register(unit, dev string, now int64) error {
	if unit == "" || dev == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return probe.ErrClockBack
	}
	if n.units[unit] != nil {
		return probe.ErrConflict
	}
	if len(n.devs[dev]) == 0 {
		return probe.ErrNotFound
	}
	n.units[unit] = &nUnit{reg: now, ivs: []nInterval{{dev: dev, start: now, end: -1}}}
	n.advance(now)
	return nil
}

func (n *naive) load(unit, dev string, now int64) error {
	if unit == "" || dev == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return probe.ErrClockBack
	}
	u := n.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if len(n.devs[dev]) == 0 {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	if u.ivs[len(u.ivs)-1].end == -1 {
		return probe.ErrState
	}
	u.ivs = append(u.ivs, nInterval{dev: dev, start: now, end: -1})
	n.advance(now)
	return nil
}

func (n *naive) unload(unit, dev string, now int64) error {
	if unit == "" || dev == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return probe.ErrClockBack
	}
	u := n.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if len(n.devs[dev]) == 0 {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	last := &u.ivs[len(u.ivs)-1]
	if last.end != -1 || last.dev != dev {
		return probe.ErrState
	}
	last.end = now
	n.advance(now)
	return nil
}

func (n *naive) evaluate(unit string, now int64) (budget.Result, error) {
	if unit == "" || !probe.ValidNow(now) {
		return budget.Result{}, probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return budget.Result{}, probe.ErrClockBack
	}
	u := n.units[unit]
	if u == nil {
		return budget.Result{}, probe.ErrNotFound
	}
	n.advance(now)
	if u.released {
		return budget.Result{E: u.frozen}, nil
	}
	e := n.exposure(u, now)
	at, ok := n.spoiledAt(u, now)
	return budget.Result{E: e, Spoiled: e > n.bmax, SpoiledAt: at, HasSpoiledAt: ok}, nil
}

func (n *naive) release(unit string, op release.Operator, now int64) error {
	if unit == "" || op.Name == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	if now < n.maxNow {
		return probe.ErrClockBack
	}
	if op.Perms&release.PermRelease == 0 {
		return probe.ErrNoReleasePerm
	}
	u := n.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	e := n.exposure(u, now)
	if e > n.bmax {
		return probe.ErrSpoiled
	}
	if e*100 >= n.bmax*n.q && op.Perms&release.PermQA == 0 {
		return probe.ErrNoQAPerm
	}
	u.released = true
	u.frozen = e
	if last := &u.ivs[len(u.ivs)-1]; last.end == -1 {
		last.end = now
	}
	n.advance(now)
	return nil
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// 1500 组随机操作序列与逐分钟朴素模拟对照，日志打印输入、输出与判定依据。
func TestRandomFuzzNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	devs := []string{"D0", "D1"}
	units := []string{"U0", "U1", "U2"}
	permOpts := []release.Perm{0, release.PermRelease, release.PermRelease | release.PermQA}
	for group := 0; group < 1500; group++ {
		hi := int64(rng.Intn(201) - 50)
		delta := int64(1 + rng.Intn(50))
		w := int64(2 + rng.Intn(9))
		g := int64(1 + rng.Intn(20))
		h := int64(1 + rng.Intn(10))
		bmax := int64(1 + rng.Intn(300))
		q := int64(1 + rng.Intn(100))
		s := newSystem(t,
			probe.Config{Hi: hi, Delta: delta, W: w, G: g},
			budget.Config{H: h, Bmax: bmax, Q: q})
		nv := newNaive(hi, delta, w, g, h, bmax, q)
		t.Logf("组%d 输入参数: Hi=%d Delta=%d w=%d G=%d H=%d Bmax=%d Q=%d",
			group, hi, delta, w, g, h, bmax, q)
		now := int64(0)
		ops := 25 + rng.Intn(30)
		for i := 0; i < ops; i++ {
			switch r := rng.Intn(10); {
			case r < 7:
				now += int64(rng.Intn(4))
			case r == 7: // 保持不变
			case r == 8:
				now -= int64(1 + rng.Intn(3)) // 小幅回退，可能为负
			default:
				now -= int64(rng.Intn(20)) // 大幅回退
			}
			var gErr, nErr error
			var gRes, nRes budget.Result
			isEval := false
			var desc string
			switch r := rng.Intn(100); {
			case r < 30:
				dev := devs[rng.Intn(len(devs))]
				if rng.Intn(25) == 0 {
					dev = ""
				}
				temp := hi + int64(rng.Intn(81)) - 30
				desc = fmt.Sprintf("Reading(%q,%d,%d)", dev, temp, now)
				gErr = s.probe.Reading(dev, temp, now)
				nErr = nv.reading(dev, temp, now)
			case r < 42:
				u := units[rng.Intn(len(units))]
				dev := devs[rng.Intn(len(devs))]
				if rng.Intn(25) == 0 {
					dev = "DX"
				}
				desc = fmt.Sprintf("Register(%q,%q,%d)", u, dev, now)
				gErr = s.release.Register(u, dev, now)
				nErr = nv.register(u, dev, now)
			case r < 54:
				u := units[rng.Intn(len(units))]
				dev := devs[rng.Intn(len(devs))]
				desc = fmt.Sprintf("Unload(%q,%q,%d)", u, dev, now)
				gErr = s.release.Unload(u, dev, now)
				nErr = nv.unload(u, dev, now)
			case r < 66:
				u := units[rng.Intn(len(units))]
				dev := devs[rng.Intn(len(devs))]
				desc = fmt.Sprintf("Load(%q,%q,%d)", u, dev, now)
				gErr = s.release.Load(u, dev, now)
				nErr = nv.load(u, dev, now)
			case r < 84:
				u := units[rng.Intn(len(units))]
				if rng.Intn(30) == 0 {
					u = ""
				}
				desc = fmt.Sprintf("Evaluate(%q,%d)", u, now)
				gRes, gErr = s.eval.Evaluate(u, now)
				nRes, nErr = nv.evaluate(u, now)
				isEval = true
			default:
				u := units[rng.Intn(len(units))]
				op := release.Operator{Name: "op", Perms: permOpts[rng.Intn(len(permOpts))]}
				if rng.Intn(30) == 0 {
					op.Name = ""
				}
				desc = fmt.Sprintf("Release(%q,%+v,%d)", u, op, now)
				gErr = s.release.Release(u, op, now)
				nErr = nv.release(u, op, now)
			}
			if !sameErr(gErr, nErr) {
				t.Fatalf("组%d 第%d步 %s: 实现 err=%v, 朴素 err=%v", group, i, desc, gErr, nErr)
			}
			if isEval && gErr == nil {
				if gRes != nRes {
					t.Fatalf("组%d 第%d步 %s: 实现=%+v, 朴素=%+v", group, i, desc, gRes, nRes)
				}
				t.Logf("组%d 第%d步 %s -> E=%d Spoiled=%v SpoiledAt=(%d,%v) 判定依据: 与逐分钟朴素模拟一致",
					group, i, desc, gRes.E, gRes.Spoiled, gRes.SpoiledAt, gRes.HasSpoiledAt)
			} else {
				t.Logf("组%d 第%d步 %s -> err=%v 判定依据: 拒绝类别一致", group, i, desc, gErr)
			}
		}
		// 组末对所有单元在最大时钟之后做终评，覆盖未结束空档。
		finalNow := nv.maxNow + 5
		for _, u := range units {
			gRes, gErr := s.eval.Evaluate(u, finalNow)
			nRes, nErr := nv.evaluate(u, finalNow)
			if !sameErr(gErr, nErr) {
				t.Fatalf("组%d 终评 %s: 实现 err=%v, 朴素 err=%v", group, u, gErr, nErr)
			}
			if gErr == nil && gRes != nRes {
				t.Fatalf("组%d 终评 %s: 实现=%+v, 朴素=%+v", group, u, gRes, nRes)
			}
		}
	}
}
