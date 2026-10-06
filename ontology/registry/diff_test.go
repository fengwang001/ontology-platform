package registry

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// model 为两种实现共同满足的接口。
type model interface {
	RegisterFacility(id, holder string, startPeriod int64) *Error
	SetQualificationEnd(id string, endPeriod int64) *Error
	RevokeQualificationEnd(id string) *Error
	RegisterMeter(id string, genPeriod, qty int64) *Error
	Remainder(id string) (int64, *Error)
	Transfer(from, to string, serials []int64) *Error
	RegisterConsumption(id string, usePeriod, qty int64) *Error
	Declare(consumer string, usePeriod int64, serials []int64) *Error
	CancelledQty(consumer string, usePeriod int64) int64
	Cert(serial int64) *Cert
	CertCount() int
	LastSerial() int64
	Events() []Event
}

type diffEnv struct {
	t       *testing.T
	rng     *rand.Rand
	facs    []string
	holders []string
	cons    []string
	maxAge  int64
	last    int64
}

func newDiffEnv(t *testing.T, rng *rand.Rand, maxAge int64) *diffEnv {
	return &diffEnv{
		t:       t,
		rng:     rng,
		facs:    []string{"F1", "F2", "F3"},
		holders: []string{"H1", "H2", "H3", "H4"},
		cons:    []string{"H1", "H2", "H3", "H4"},
		maxAge:  maxAge,
		last:    40,
	}
}

func (e *diffEnv) period() int64 { return int64(1 + e.rng.Intn(30)) }

func (e *diffEnv) facility() string { return e.facs[e.rng.Intn(len(e.facs))] }
func (e *diffEnv) holder() string   { return e.holders[e.rng.Intn(len(e.holders))] }

// op 为一条可在两个模型上重放的输入。
type op struct {
	name  string
	apply func(m model) *Error
}

func (e *diffEnv) randomOp(step int) op {
	switch e.rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14:
		// 计量登记/修正（含回溯缩小触发撤销）
		f := e.facility()
		p := e.period()
		q := int64(e.rng.Intn(6))
		return op{
			name:  fmt.Sprintf("RegisterMeter(%s,p=%d,q=%d)", f, p, q),
			apply: func(m model) *Error { return m.RegisterMeter(f, p, q) },
		}
	case 15, 16, 17, 18, 19, 20, 21, 22, 23:
		c := e.holder()
		p := e.period()
		q := int64(1 + e.rng.Intn(8))
		return op{
			name:  fmt.Sprintf("RegisterConsumption(%s,p=%d,q=%d)", c, p, q),
			apply: func(m model) *Error { return m.RegisterConsumption(c, p, q) },
		}
	case 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38:
		// 转让：从既有证书中取一批
		return e.transferOp()
	case 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53:
		return e.declareOp()
	case 54, 55, 56, 57:
		return e.qualEndOp(false)
	case 58:
		return e.qualEndOp(true)
	default:
		// 兜底：再做一次计量
		f := e.facility()
		p := e.period()
		q := int64(e.rng.Intn(6))
		return op{
			name:  fmt.Sprintf("RegisterMeter(%s,p=%d,q=%d)", f, p, q),
			apply: func(m model) *Error { return m.RegisterMeter(f, p, q) },
		}
	}
}

func (e *diffEnv) transferOp() op {
	from, to := e.holder(), e.holder()
	k := 1 + e.rng.Intn(3)
	// 序号在构造时一次选定，两个模型共享同一批输入。
	serials := e.randomSerials(k)
	return op{
		name: fmt.Sprintf("Transfer(%s->%s,%v)", from, to, serials),
		apply: func(m model) *Error {
			if len(serials) == 0 {
				return nil
			}
			return m.Transfer(from, to, serials)
		},
	}
}

func (e *diffEnv) declareOp() op {
	c := e.holder()
	p := e.period()
	k := 1 + e.rng.Intn(3)
	serials := e.randomSerials(k)
	return op{
		name: fmt.Sprintf("Declare(%s,p=%d,%v)", c, p, serials),
		apply: func(m model) *Error {
			if len(serials) == 0 {
				return nil
			}
			return m.Declare(c, p, serials)
		},
	}
}

// randomSerials 在操作构造时抽样；它只依赖序号空间（两模型已同步），
// 不依赖具体模型，因此两个模型重放的是完全相同的输入。
func (e *diffEnv) randomSerials(k int) []int64 {
	last := e.last
	var picks []int64
	seen := map[int64]bool{}
	for tries := 0; tries < 3*k+3 && len(picks) < k; tries++ {
		s := int64(1 + e.rng.Intn(int(last)))
		if !seen[s] {
			seen[s] = true
			picks = append(picks, s)
		}
	}
	return picks
}

func (e *diffEnv) qualEndOp(revoke bool) op {
	f := e.facility()
	if revoke {
		return op{name: "RevokeQualificationEnd(" + f + ")",
			apply: func(m model) *Error { return m.RevokeQualificationEnd(f) }}
	}
	end := int64(1 + e.rng.Intn(32))
	return op{name: fmt.Sprintf("SetQualificationEnd(%s,end=%d)", f, end),
		apply: func(m model) *Error { return m.SetQualificationEnd(f, end) }}
}

func sameError(a, b *Error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Code == b.Code && a.Serial == b.Serial
}

func snapshot(m model) map[int64]*Cert {
	out := map[int64]*Cert{}
	for s := int64(1); s <= m.LastSerial(); s++ {
		if c := m.Cert(s); c != nil {
			cp := *c
			out[s] = &cp
		}
	}
	return out
}

func consumptionSnapshot(m model) map[string]int64 {
	out := map[string]int64{}
	// 用证书扫描所有出现过的用电方/用电期，并额外探测若干期。
	for s := int64(1); s <= m.LastSerial(); s++ {
		c := m.Cert(s)
		if c == nil || c.Status == StatusHeld {
			continue
		}
		key := c.Consumer + ":" + fmt.Sprint(c.UsePeriod)
		out[key] = m.CancelledQty(c.Consumer, c.UsePeriod)
	}
	return out
}

// TestRandomDifferential 用同一随机序列驱动主实现与朴素模型，逐步比对
// 拒绝类别/失败序号、证书全量快照、有效注销量、序号与事件序列。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			maxAge := int64(rng.Intn(5))
			var log strings.Builder
			a := New(Config{UnitQty: 1, MaxAgePeriods: maxAge, MinPeriod: 1, MaxPeriod: 40}, &log)
			b := NewNaive(Config{UnitQty: 1, MaxAgePeriods: maxAge, MinPeriod: 1, MaxPeriod: 40}, &log)

			// 相同的设施登记引导序列。
			for _, f := range []string{"F1", "F2", "F3"} {
				start := int64(1 + rng.Intn(4))
				fmt.Fprintf(&log, "IN  RegisterFacility(%s,start=%d)\n", f, start)
				if ea, eb := a.RegisterFacility(f, f, start), b.RegisterFacility(f, f, start); !sameError(ea, eb) {
					t.Fatalf("seed=%d 设施登记分歧 %v vs %v", seed, ea, eb)
				}
			}

			env := newDiffEnv(t, rng, maxAge)
			const steps = 350
			for step := 0; step < steps; step++ {
				o := env.randomOp(step)
				fmt.Fprintf(&log, "IN  %s\n", o.name)
				ea, eb := o.apply(a), o.apply(b)
				fmt.Fprintf(&log, "OUT main=%v naive=%v\n", ea, eb)
				if !sameError(ea, eb) {
					t.Fatalf("seed=%d step=%d %s 拒绝分歧: main=%v naive=%v\n%s",
						seed, step, o.name, ea, eb, log.String())
				}
				if ea == nil {
					if a.LastSerial() != b.LastSerial() {
						t.Fatalf("seed=%d step=%d 序号分歧 %d vs %d", seed, step, a.LastSerial(), b.LastSerial())
					}
					if !reflect.DeepEqual(snapshot(a), snapshot(b)) {
						t.Fatalf("seed=%d step=%d %s 证书快照分歧\n%s", seed, step, o.name, log.String())
					}
					if !reflect.DeepEqual(consumptionSnapshot(a), consumptionSnapshot(b)) {
						t.Fatalf("seed=%d step=%d %s 有效注销量分歧\n%s", seed, step, o.name, log.String())
					}
					if !reflect.DeepEqual(a.Events(), b.Events()) {
						t.Fatalf("seed=%d step=%d %s 事件序列分歧", seed, step, o.name)
					}
					checkInvariants(t, a, seed, step)
				}
			}
			if t.Failed() {
				t.Log("\n" + log.String())
			}
		})
	}
}

// checkInvariants 校验主实现的关键不变量。
func checkInvariants(t *testing.T, r *Registry, seed int64, step int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	// 序号连续无洞。
	if int64(len(r.certs)) > r.serial {
		t.Fatalf("seed=%d step=%d 序号有洞", seed, step)
	}
	for s := int64(1); s <= r.serial; s++ {
		if r.certs[s] == nil {
			t.Fatalf("seed=%d step=%d 序号%d缺失", seed, step, s)
		}
	}
	// 有效注销量 <= 登记用电量；计量不变量：(持有+已注销)*单位 + 余量 == 最新电量(+并入余量)。
	for id, c := range r.cons {
		for p, cp := range c.periods {
			if cp.cancelled > cp.qty {
				t.Fatalf("seed=%d 用电方%s期%d 注销量%d>用电量%d", seed, id, p, cp.cancelled, cp.qty)
			}
		}
	}
	for id, f := range r.facs {
		for p, fp := range f.periods {
			nonRevoked := 0
			for _, c := range r.certs {
				if c.Facility == id && c.Period == p && c.Status != StatusRevoked {
					nonRevoked++
				}
			}
			if nonRevoked != fp.active {
				t.Fatalf("seed=%d %s期%d active=%d 实扫=%d", seed, id, p, fp.active, nonRevoked)
			}
			got := int64(nonRevoked)*r.cfg.UnitQty + fp.outRem
			if got != fp.qty+fp.inRem {
				t.Fatalf("seed=%d %s期%d 计量不变量 %d != qty%d+inRem%d",
					seed, id, p, got, fp.qty, fp.inRem)
			}
		}
	}
}

// BenchmarkTransferIsBatchOnly 20 万张证书的登记簿上转让 100 张，
// 开销应只与 100 相关：重复跑两次（登记簿规模翻倍）耗时不应随之线性增长。
func BenchmarkTransferIsBatchOnly(b *testing.B) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 1000000, MinPeriod: 1, MaxPeriod: 1000000}, nil)
	if e := r.RegisterFacility("F", "H", 1); e != nil {
		b.Fatal(e)
	}
	const periods = 100
	for p := int64(1); p <= periods; p++ {
		if e := r.RegisterMeter("F", p, 2000); e != nil {
			b.Fatal(e)
		}
	}
	batch := make([]int64, 100)
	for i := range batch {
		batch[i] = int64(1 + i*2000) // 散布各期，仍是 H 持有
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if e := r.Transfer("H", "H2", batch); e != nil {
			b.Fatal(e)
		}
		if e := r.Transfer("H2", "H", batch); e != nil {
			b.Fatal(e)
		}
	}
}

// 小规模对照：1000 张证书上同样转让 100 张，
// 与 20 万张规模的 ns/op 同量级，即开销不随总证书数增长（见设计说明）。
func BenchmarkTransferSmallRegistry(b *testing.B) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 1000000, MinPeriod: 1, MaxPeriod: 1000000}, nil)
	if e := r.RegisterFacility("F", "H", 1); e != nil {
		b.Fatal(e)
	}
	for p := int64(1); p <= 100; p++ {
		if e := r.RegisterMeter("F", p, 10); e != nil {
			b.Fatal(e)
		}
	}
	batch := make([]int64, 100)
	for i := range batch {
		batch[i] = int64(1 + i*10)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if e := r.Transfer("H", "H2", batch); e != nil {
			b.Fatal(e)
		}
		if e := r.Transfer("H2", "H", batch); e != nil {
			b.Fatal(e)
		}
	}
}

// 撤销局部性：每轮重建 N 张规模，撤销其中一期一半。
// 对比 N=20000 与 N=2000 的 ns/op，撤销 1000 张部分应同量级（差异主要来自建账）。
func benchmarkRevoke(b *testing.B, periods int64, perPeriod int64) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 1000000, MinPeriod: 1, MaxPeriod: 1000000}, nil)
	if e := r.RegisterFacility("F", "H", 1); e != nil {
		b.Fatal(e)
	}
	for p := int64(1); p <= periods; p++ {
		if e := r.RegisterMeter("F", p, perPeriod); e != nil {
			b.Fatal(e)
		}
	}
	// 只对第一期做撤销，量为该期一半；循环内把第一期反复升降修正。
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if e := r.RegisterMeter("F", 1, perPeriod/2); e != nil {
			b.Fatal(e)
		}
		if e := r.RegisterMeter("F", 1, perPeriod); e != nil {
			b.Fatal(e)
		}
	}
}

func BenchmarkRevokeLargePeriod(b *testing.B) { benchmarkRevoke(b, 100, 2000) }
func BenchmarkRevokeSmallTotal(b *testing.B)  { benchmarkRevoke(b, 100, 20) }
