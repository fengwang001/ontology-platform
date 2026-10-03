package picker

import (
	"errors"
	"testing"
)

func newTestPicker(t *testing.T, cfg Config) *Picker {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// TestEWMADecay 表驱动覆盖先验、衰减边界夹取、峰值抬升/不下降、失败 Pf。
func TestEWMADecay(t *testing.T) {
	cases := []struct {
		name string
		ops  func(p *Picker)
		want func(t *testing.T, p *Picker)
	}{
		{
			"no sample reads prior",
			func(p *Picker) {},
			func(t *testing.T, p *Picker) {
				if v, _ := p.ValueAt("a", 0); v != 50 {
					t.Fatalf("val=%d want P0=50", v)
				}
				if v, _ := p.ValueAt("a", 99999); v != 50 {
					t.Fatalf("val=%d want P0 unchanged", v)
				}
			},
		},
		{
			"decay zero at d=tau and clamped beyond",
			func(p *Picker) {
				if err := p.Release(mustPickID(t, p, 0), 200, true, 0); err != nil {
					t.Fatal(err)
				}
			},
			func(t *testing.T, p *Picker) {
				if v, _ := p.ValueAt("a", 50); v != 100 {
					t.Fatalf("d=50 val=%d want 100", v)
				}
				if v, _ := p.ValueAt("a", 100); v != 0 {
					t.Fatalf("d=tau val=%d want 0", v)
				}
				if v, _ := p.ValueAt("a", 1000); v != 0 {
					t.Fatalf("d>tau clamped val=%d want 0", v)
				}
			},
		},
		{
			"lower sample stores decayed value 180",
			func(p *Picker) {
				if err := p.Release(mustPickID(t, p, 0), 200, true, 0); err != nil {
					t.Fatal(err)
				}
				if err := p.Release(mustPickID(t, p, 10), 10, true, 10); err != nil {
					t.Fatal(err)
				}
			},
			func(t *testing.T, p *Picker) {
				if v, _ := p.ValueAt("a", 10); v != 180 {
					t.Fatalf("val=%d want max(10,180)=180", v)
				}
			},
		},
		{
			"higher sample raises peak",
			func(p *Picker) {
				if err := p.Release(mustPickID(t, p, 0), 200, true, 0); err != nil {
					t.Fatal(err)
				}
				if err := p.Release(mustPickID(t, p, 20), 500, true, 20); err != nil {
					t.Fatal(err)
				}
			},
			func(t *testing.T, p *Picker) {
				if v, _ := p.ValueAt("a", 20); v != 500 {
					t.Fatalf("val=%d want 500", v)
				}
			},
		},
		{
			"failure samples Pf ignoring rtt",
			func(p *Picker) {
				if err := p.Release(mustPickID(t, p, 0), 1, false, 0); err != nil {
					t.Fatal(err)
				}
			},
			func(t *testing.T, p *Picker) {
				if v, _ := p.ValueAt("a", 0); v != 1000 {
					t.Fatalf("val=%d want Pf=1000", v)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPicker(t, baseCfg())
			mustAdd(t, p, "a")
			tc.ops(p)
			tc.want(t, p)
		})
	}
}

// TestP2CIndices 表驱动检查 n=1 与 n=2、n=3 的抽号公式及并列取小 id。
func TestP2CIndices(t *testing.T) {
	cases := []struct {
		name   string
		ids    []string
		r1, r2 uint64
		want   string
	}{
		{"n=1 sole eligible", []string{"a"}, 9, 9, "a"},
		{"n=2 i=0 j=1", []string{"a", "b"}, 0, 0, "a"},
		{"n=2 i=1 j=0 tie smaller id", []string{"a", "b"}, 1, 0, "a"},
		{"n=3 i=0 j=1", []string{"a", "b", "c"}, 0, 0, "a"},
		{"n=3 i=2 j=0 tie smaller a", []string{"a", "b", "c"}, 2, 0, "a"},
		{"unsorted input sorted by bytes", []string{"c", "b", "a"}, 0, 0, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPicker(t, baseCfg())
			mustAdd(t, p, tc.ids...)
			_, ep := mustPick(t, p, 0, tc.r1, tc.r2)
			if ep != tc.want {
				t.Fatalf("winner=%q want %q", ep, tc.want)
			}
		})
	}
}

// TestIndicesAlwaysDistinct 穷举小规模 n 与 r1,r2，验证 i≠j 恒成立。
func TestIndicesAlwaysDistinct(t *testing.T) {
	for n := 2; n <= 32; n++ {
		for r1 := uint64(0); r1 < uint64(n)*2; r1++ {
			for r2 := uint64(0); r2 < uint64(n)*2; r2++ {
				i := int(r1 % uint64(n))
				j := int((uint64(i) + 1 + r2%uint64(n-1)) % uint64(n))
				if i == j {
					t.Fatalf("n=%d r1=%d r2=%d i=j=%d", n, r1, r2, i)
				}
			}
		}
	}
}

// TestSaturation 满员端点不入候选；全满报饱和；无端点报无端点；被拒不耗号。
func TestSaturation(t *testing.T) {
	p := newTestPicker(t, baseCfg())
	if _, _, err := p.Pick(0, 0, 0); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("empty pick = %v, want ErrNoEndpoint", err)
	}
	mustAdd(t, p, "a")
	tk1, _ := mustPick(t, p, 0, 0, 0)
	mustPick(t, p, 0, 0, 0)
	if _, _, err := p.Pick(0, 0, 0); !errors.Is(err, ErrSaturated) {
		t.Fatalf("saturated pick = %v, want ErrSaturated", err)
	}
	if err := p.Release(tk1, 10, true, 1); err != nil {
		t.Fatal(err)
	}
	tk3, ep := mustPick(t, p, 1, 0, 0)
	if tk3 != 3 {
		t.Fatalf("ticket after rejected pick = %d, want 3", tk3)
	}
	if ep != "a" {
		t.Fatalf("winner=%q want a", ep)
	}
}

// TestDrainingRelease 排空端点不被选但可 Release，归零移除并释放名额。
func TestDrainingRelease(t *testing.T) {
	cfg := baseCfg()
	cfg.MaxEndpoints = 2
	p := newTestPicker(t, cfg)
	mustAdd(t, p, "a", "b")
	tk, _ := mustPick(t, p, 0, 0, 0)
	if err := p.RemoveEndpoint("a"); err != nil {
		t.Fatal(err)
	}
	if !p.IsDraining("a") {
		t.Fatal("a should be draining")
	}
	if err := p.AddEndpoint("c"); !errors.Is(err, ErrFull) {
		t.Fatalf("add while draining slot held = %v, want ErrFull", err)
	}
	_, ep := mustPick(t, p, 5, 7, 3)
	if ep != "b" {
		t.Fatalf("pick=%q want b only", ep)
	}
	if err := p.Release(tk, 12, true, 6); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Inflight("a"); ok {
		t.Fatal("a should be removed after drain release")
	}
	if err := p.AddEndpoint("c"); err != nil {
		t.Fatalf("add c after slot freed: %v", err)
	}
}

// TestTicketLifecycle 未知/重复归还被拒，且不改变账本。
func TestTicketLifecycle(t *testing.T) {
	p := newTestPicker(t, baseCfg())
	mustAdd(t, p, "a")
	if err := p.Release(999, 10, true, 0); !errors.Is(err, ErrTicket) {
		t.Fatalf("unknown ticket = %v, want ErrTicket", err)
	}
	tk, _ := mustPick(t, p, 0, 0, 0)
	if err := p.Release(tk, 10, true, 1); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(tk, 10, true, 2); !errors.Is(err, ErrTicket) {
		t.Fatalf("double release = %v, want ErrTicket", err)
	}
	if infl, _ := p.Inflight("a"); infl != 0 {
		t.Fatalf("infl=%d want 0", infl)
	}
	if p.OpenTickets() != 0 {
		t.Fatalf("open=%d want 0", p.OpenTickets())
	}
}

// TestValidationOrder 校验拒绝优先级：参数 > 时间 > 时钟回拨 > 其余。
func TestValidationOrder(t *testing.T) {
	p := newTestPicker(t, baseCfg())
	mustAdd(t, p, "a")
	tk, _ := mustPick(t, p, 100, 0, 0)

	// rtt 非法优先于时间非法与回拨。
	if err := p.Release(tk, 1<<31, true, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("rtt+time = %v, want ErrInvalidParam", err)
	}
	// 时间非法优先于回拨。
	if err := p.Release(tk, 10, true, maxNow+1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("oversized now = %v, want ErrInvalidTime", err)
	}
	if err := p.Release(tk, 10, true, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("negative now = %v, want ErrInvalidTime", err)
	}
	// 回拨（10 < 已见 100）优先于票据归还逻辑。
	if err := p.Release(tk, 10, true, 50); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("backward = %v, want ErrClockBackward", err)
	}
	// 票据仍未归还（之前拒绝均无效）。
	if p.OpenTickets() != 1 {
		t.Fatalf("open=%d want 1 (rejected releases had no effect)", p.OpenTickets())
	}
	// 空 id 只报参数非法。
	if err := p.AddEndpoint(""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("add empty = %v", err)
	}
	if err := p.RemoveEndpoint(""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("remove empty = %v", err)
	}
	// Add/Remove 其余错误。
	if err := p.AddEndpoint("a"); !errors.Is(err, ErrExists) {
		t.Fatalf("dup add = %v", err)
	}
	if err := p.RemoveEndpoint("zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing remove = %v", err)
	}
	// 满员（Nmax=1，当前 a）。
	_ = p.Release(tk, 10, true, 100)
	if err := p.RemoveEndpoint("a"); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, p, "a")
	// Nmax 默认 10，这里另测 Nmax=1。
}

// TestConfigValidation 构造参数边界。
func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{Tau: 0, Prior: 50, FailPenalty: 1000, MaxInflight: 2, MaxEndpoints: 10},
		{Tau: 100, Prior: 0, FailPenalty: 1000, MaxInflight: 2, MaxEndpoints: 10},
		{Tau: 100, Prior: 50, FailPenalty: 1 << 40, MaxInflight: 2, MaxEndpoints: 10},
		{Tau: 100, Prior: 50, FailPenalty: 1000, MaxInflight: 0, MaxEndpoints: 10},
		{Tau: 100, Prior: 50, FailPenalty: 1000, MaxInflight: 2, MaxEndpoints: 0},
		{Tau: 100, Prior: 50, FailPenalty: 1000, MaxInflight: maxM + 1, MaxEndpoints: 10},
		{Tau: 100, Prior: 50, FailPenalty: 1000, MaxInflight: 2, MaxEndpoints: maxN + 1},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: err=%v want ErrInvalidParam", i, err)
		}
	}
}

// TestNmaxFull 专门验证 Nmax 边界。
func TestNmaxFull(t *testing.T) {
	cfg := baseCfg()
	cfg.MaxEndpoints = 1
	p := newTestPicker(t, cfg)
	mustAdd(t, p, "a")
	if err := p.AddEndpoint("b"); !errors.Is(err, ErrFull) {
		t.Fatalf("nmax add = %v want ErrFull", err)
	}
}

func baseCfg() Config {
	return Config{Tau: 100, Prior: 50, FailPenalty: 1000, MaxInflight: 2, MaxEndpoints: 10}
}

func mustAdd(t *testing.T, p *Picker, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := p.AddEndpoint(id); err != nil {
			t.Fatalf("AddEndpoint(%q): %v", id, err)
		}
	}
}

func mustPick(t *testing.T, p *Picker, now int64, r1, r2 uint64) (int64, string) {
	t.Helper()
	tk, ep, err := p.Pick(now, r1, r2)
	if err != nil {
		t.Fatalf("Pick(now=%d,r1=%d,r2=%d): %v", now, r1, r2, err)
	}
	return tk, ep
}

func mustPickID(t *testing.T, p *Picker, now int64) int64 {
	t.Helper()
	tk, _, err := p.Pick(now, 0, 0)
	if err != nil {
		t.Fatalf("pick at %d: %v", now, err)
	}
	return tk
}

// TestSpecWalkthrough 逐步复现题目给出的完整示例。
func TestSpecWalkthrough(t *testing.T) {
	p := newTestPicker(t, baseCfg())
	mustAdd(t, p, "a", "b", "c")

	tk1, ep := mustPick(t, p, 0, 0, 0)
	if tk1 != 1 || ep != "a" {
		t.Fatalf("pick#1 = (%d,%q), want (1,a)", tk1, ep)
	}
	tk2, ep := mustPick(t, p, 0, 0, 0)
	if tk2 != 2 || ep != "b" {
		t.Fatalf("pick#2 = (%d,%q), want (2,b)", tk2, ep)
	}

	if err := p.Release(1, 200, true, 10); err != nil {
		t.Fatalf("release 1: %v", err)
	}
	if v, _ := p.ValueAt("a", 10); v != 200 {
		t.Fatalf("a est at 10 = %d, want 200", v)
	}

	tk3, ep := mustPick(t, p, 10, 0, 0)
	if tk3 != 3 || ep != "b" {
		t.Fatalf("pick#3 = (%d,%q), want (3,b)", tk3, ep)
	}

	tk4, ep := mustPick(t, p, 60, 0, 0)
	if tk4 != 4 || ep != "c" {
		t.Fatalf("pick#4 = (%d,%q), want (4,c)", tk4, ep)
	}
	if v, _ := p.ValueAt("a", 60); v != 100 {
		t.Fatalf("a decayed val at 60 = %d, want 100", v)
	}

	if err := p.Release(3, 5, false, 70); err != nil {
		t.Fatalf("release 3 fail: %v", err)
	}
	if v, _ := p.ValueAt("b", 70); v != 1000 {
		t.Fatalf("b failed sample = %d, want Pf=1000", v)
	}

	if err := p.RemoveEndpoint("a"); err != nil {
		t.Fatalf("remove a: %v", err)
	}
	if _, ok := p.ValueAt("a", 70); ok {
		t.Fatal("a should be gone immediately")
	}
	if err := p.RemoveEndpoint("b"); err != nil {
		t.Fatalf("remove b (draining): %v", err)
	}
	if !p.IsDraining("b") {
		t.Fatal("b should be draining")
	}
	if err := p.RemoveEndpoint("b"); !errors.Is(err, ErrDraining) {
		t.Fatalf("repeat remove b = %v, want ErrDraining", err)
	}
	if err := p.AddEndpoint("b"); !errors.Is(err, ErrExists) {
		t.Fatalf("add draining b = %v, want ErrExists", err)
	}
	if n := p.NumEndpoints(); n != 2 {
		t.Fatalf("endpoints count = %d, want 2 (b,c)", n)
	}
	_, ep5 := mustPick(t, p, 75, 0, 0)
	if ep5 != "c" {
		t.Fatalf("pick while b draining = %q, want c", ep5)
	}
	if err := p.Release(2, 30, true, 80); err != nil {
		t.Fatalf("release 2: %v", err)
	}
	if _, ok := p.ValueAt("b", 80); ok {
		t.Fatal("b should be removed after drain to zero")
	}
	if infl, _ := p.Inflight("c"); infl != 2 {
		t.Fatalf("c inflight = %d, want 2", infl)
	}
	if p.OpenTickets() != 2 {
		t.Fatalf("open tickets = %d, want 2", p.OpenTickets())
	}
}
