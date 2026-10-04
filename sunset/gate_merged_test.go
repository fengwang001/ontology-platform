package sunset

import (
	"errors"
	"reflect"
	"testing"

	"ontology/registry"
)

func exampleCfg() Config {
	return Config{Nmin: 100, Bw: 60, Pd: 20, X: 5, Q: 30, Xmax: 100}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewConfig(t *testing.T) {
	base := exampleCfg()
	if _, err := New(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"zero Nmin", func(c *Config) { c.Nmin = 0 }},
		{"negative Bw", func(c *Config) { c.Bw = -1 }},
		{"Bw > Nmin", func(c *Config) { c.Bw = 101 }},
		{"X > Pd", func(c *Config) { c.X = 21 }},
		{"zero Q", func(c *Config) { c.Q = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mut(&c)
			if _, err := New(c); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

// TestSpecExample 复现题给示例时间线。
func TestSpecExample(t *testing.T) {
	g, err := New(exampleCfg())
	must(t, err)
	must(t, g.AddDataset("d", nil, 0))

	affected, err := g.Deprecate("d", 100, 0)
	must(t, err)
	if len(affected) != 0 {
		t.Fatalf("affected = %v", affected)
	}
	if sun, brown, ok := g.Times("d"); !ok || sun != 100 || brown != 40 {
		t.Fatalf("times = %d %d %v", sun, brown, ok)
	}

	res, err := g.Access("c1", "d", 10)
	if err != nil || !res.Allowed {
		t.Fatalf("access t10: %+v %v", res, err)
	}
	if err := g.Advance("d", 39); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("advance 39 err=%v", err)
	}
	must(t, g.Advance("d", 40))
	if phase, _ := g.Phase("d"); phase != registry.Brownout {
		t.Fatalf("phase = %v", phase)
	}

	type ac struct {
		t        int64
		consumer string
		allowed  bool
	}
	accesses := []ac{
		{44, "c1", false}, {45, "c1", true},
		{69, "c1", false}, {70, "c1", true},
		{94, "c2", false}, {95, "c2", true},
	}
	for _, a := range accesses {
		res, err := g.Access(a.consumer, "d", a.t)
		if a.allowed {
			if err != nil || !res.Allowed || !res.Warning {
				t.Fatalf("access %d: %+v %v", a.t, res, err)
			}
		} else if !errors.Is(err, ErrBrownout) {
			t.Fatalf("access %d err=%v want ErrBrownout", a.t, err)
		}
	}

	if err := g.Advance("d", 99); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("advance 99 err=%v", err)
	}
	err = g.Advance("d", 100)
	if !errors.Is(err, ErrConsumers) {
		t.Fatalf("advance 100 err=%v", err)
	}
	if items := ErrorItems(err); !reflect.DeepEqual(items, []string{"c2"}) {
		t.Fatalf("blocking = %v", items)
	}

	must(t, g.Ack("c2", "d", 101))
	must(t, g.Advance("d", 101))
	if phase, _ := g.Phase("d"); phase != registry.Retired {
		t.Fatalf("phase = %v", phase)
	}
}

func TestNoAckInactivity(t *testing.T) {
	// c2 最后成功访问 95；cycle3 起整周期拒绝，124 仍活跃(95>94)，125 不活跃。
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Deprecate("d", 100, 0)
	must(t, g.Advance("d", 40))
	if _, err := g.Access("c2", "d", 95); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{100, 105, 119} {
		if _, err := g.Access("c2", "d", ts); !errors.Is(err, ErrBrownout) {
			t.Fatalf("cycle3 t=%d err=%v", ts, err)
		}
	}
	// 被拒访问不记 lastAccess：124/125 判定仍只基于 95。
	if err := g.Advance("d", 124); !errors.Is(err, ErrConsumers) {
		t.Fatalf("124 err=%v", err)
	}
	must(t, g.Advance("d", 125))
	if phase, _ := g.Phase("d"); phase != registry.Retired {
		t.Fatalf("phase = %v", phase)
	}
}

func TestBrownoutBoundaries(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Deprecate("d", 100, 0)
	must(t, g.Advance("d", 40))
	// shut(k)=min(5*(k+1),20)：边界两侧各取一秒。
	cases := []struct {
		t       int64
		allowed bool
	}{
		{40, false}, {44, false}, {45, true}, {59, true},
		{60, false}, {64, false}, {69, false}, {70, true}, {79, true},
		{80, false}, {89, false}, {94, false}, {95, true}, {99, true},
		{100, false}, {119, false},
	}
	for i, c := range cases {
		consumer := "b" + string(rune('a'+i))
		_, err := g.Access(consumer, "d", c.t)
		if c.allowed && err != nil {
			t.Fatalf("t=%d want allow, got %v", c.t, err)
		}
		if !c.allowed && !errors.Is(err, ErrBrownout) {
			t.Fatalf("t=%d want ErrBrownout, got %v", c.t, err)
		}
	}
}

func TestDeprecatedAfterBrownStartAllowed(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Deprecate("d", 100, 0)
	// 不 Advance：now 已过 brownStart 仍按 Deprecated 放行带 Warning。
	res, err := g.Access("c1", "d", 500)
	if err != nil || !res.Allowed || !res.Warning {
		t.Fatalf("%+v %v", res, err)
	}
	must(t, g.Advance("d", 500))
	if phase, _ := g.Phase("d"); phase != registry.Brownout {
		t.Fatalf("phase=%v", phase)
	}
	// Brownout 且 i>=4：整周期拒绝。
	if _, err := g.Access("c1", "d", 500); !errors.Is(err, ErrBrownout) {
		t.Fatalf("late brownout access err=%v", err)
	}
}

func TestAckSemantics(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Deprecate("d", 100, 0)
	must(t, g.Advance("d", 40))
	_, _ = g.Access("c1", "d", 70)
	must(t, g.Ack("c1", "d", 71))
	// 被拒访问不作废确认。
	if _, err := g.Access("c1", "d", 80); !errors.Is(err, ErrBrownout) {
		t.Fatal(err)
	}
	// 确认后 95 再成功 -> 确认作废，100 时 c1 活跃。
	if _, err := g.Access("c1", "d", 95); err != nil {
		t.Fatal(err)
	}
	err := g.Advance("d", 100)
	if !errors.Is(err, ErrConsumers) || !reflect.DeepEqual(ErrorItems(err), []string{"c1"}) {
		t.Fatalf("advance 100: %v", err)
	}
	// 从无成功访问的消费者 Ack 报 ErrNotConsumer。
	if err := g.Ack("ghost", "d", 100); !errors.Is(err, ErrNotConsumer) {
		t.Fatalf("ghost ack err=%v", err)
	}
}

func TestDownstreamBlocksBeforeConsumers(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("p", nil, 0))
	must(t, g.AddDataset("ch1", []string{"p"}, 0))
	must(t, g.AddDataset("ch2", []string{"p"}, 0))
	_, _ = g.Deprecate("p", 100, 0)
	_, _ = g.Deprecate("ch1", 100, 0)
	_, _ = g.Deprecate("ch2", 100, 0)
	must(t, g.Advance("p", 40))
	must(t, g.Advance("ch1", 40))
	must(t, g.Advance("ch2", 40))
	_, _ = g.Access("loud", "p", 95)
	err := g.Advance("p", 100)
	if !errors.Is(err, ErrDownstream) {
		t.Fatalf("err=%v", err)
	}
	if items := ErrorItems(err); !reflect.DeepEqual(items, []string{"ch1", "ch2"}) {
		t.Fatalf("items=%v", items)
	}
	must(t, g.Advance("ch1", 100))
	must(t, g.Advance("ch2", 100))
	err = g.Advance("p", 100)
	if !errors.Is(err, ErrConsumers) || !reflect.DeepEqual(ErrorItems(err), []string{"loud"}) {
		t.Fatalf("after downstream: %v", err)
	}
}

func TestDeprecateAffectedList(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("a", nil, 0))
	must(t, g.AddDataset("b", []string{"a"}, 0))
	must(t, g.AddDataset("c", []string{"a"}, 0))
	must(t, g.AddDataset("d", []string{"b"}, 0))
	must(t, g.AddDataset("gone", []string{"c"}, 0))
	_, _ = g.Deprecate("gone", 100, 0)
	must(t, g.Advance("gone", 40))
	must(t, g.Advance("gone", 100)) // gone 无消费者，Retired
	affected, err := g.Deprecate("a", 100, 100)
	must(t, err)
	if !reflect.DeepEqual(affected, []string{"b", "c", "d"}) {
		t.Fatalf("affected = %v", affected)
	}
	if _, err := g.Deprecate("a", 100, 100); !errors.Is(err, ErrPhase) {
		t.Fatalf("re-deprecate err=%v", err)
	}
	if _, err := g.Deprecate("missing", 100, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err=%v", err)
	}
}

func TestNoticeTooShort(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	if _, err := g.Deprecate("d", 99, 0); !errors.Is(err, ErrNoticeTooShort) {
		t.Fatalf("err=%v", err)
	}
	if phase, _ := g.Phase("d"); phase != registry.Active {
		t.Fatalf("phase=%v after rejected deprecate", phase)
	}
	// 恰等 Nmin 允许。
	if _, err := g.Deprecate("d", 100, 0); err != nil {
		t.Fatalf("equal Nmin: %v", err)
	}
}

func TestUndeprecateKeepsExtendCounters(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Access("c1", "d", 10)
	_, _ = g.Deprecate("d", 100, 10)
	must(t, g.Extend("d", "c1", 40, 20))
	must(t, g.Undeprecate("d", 21))
	cnt, sum, _ := g.ExtendInfo("d")
	if cnt != 1 || sum != 40 {
		t.Fatalf("counters after undeprecate: %d %d", cnt, sum)
	}
	// 重新弃用后再延 60：累计恰等 Xmax=100 允许。
	_, _ = g.Deprecate("d", 100, 30)
	_, _ = g.Access("c1", "d", 30)
	must(t, g.Extend("d", "c1", 60, 30))
	// 第三次延期：次数上限。
	if err := g.Extend("d", "c1", 1, 30); !errors.Is(err, ErrExtendLimit) {
		t.Fatalf("third extend err=%v", err)
	}
	// Active 上 Extend 报 ErrPhase。
	must(t, g.Undeprecate("d", 31))
	if err := g.Extend("d", "c1", 1, 31); !errors.Is(err, ErrPhase) {
		t.Fatalf("active extend err=%v", err)
	}
}

func TestExtendMovesBothTimes(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	_, _ = g.Access("c1", "d", 0)
	_, _ = g.Deprecate("d", 100, 0)
	must(t, g.Extend("d", "c1", 50, 20))
	if sun, brown, _ := g.Times("d"); sun != 150 || brown != 90 {
		t.Fatalf("times = %d %d", sun, brown)
	}
	// 非活跃消费者（从未访问）报 ErrNotConsumer。
	if err := g.Extend("d", "ghost", 10, 20); !errors.Is(err, ErrNotConsumer) {
		t.Fatalf("ghost extend err=%v", err)
	}
	// 累计超出 Xmax 报 ErrTooLong：40+70=110 > 100（恰等 60 已在上一用例验证）。
	g2, _ := New(exampleCfg())
	must(t, g2.AddDataset("d", nil, 0))
	_, _ = g2.Access("c1", "d", 0)
	_, _ = g2.Deprecate("d", 100, 0)
	must(t, g2.Extend("d", "c1", 40, 0))
	must(t, g2.Extend("d", "c1", 60, 0)) // 恰等 100
	// 次数已达 2，ErrExtendLimit 先于 ErrTooLong。
	if err := g2.Extend("d", "c1", 1, 0); !errors.Is(err, ErrExtendLimit) {
		t.Fatalf("err=%v", err)
	}
}

// TestRejectOrder 验证“参数非法 > 时钟回退 > 不存在 > 阶段错误 > 操作自身错误”。
func TestRejectOrder(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 10))

	// 参数非法最先（即使伴随时钟回退/不存在/阶段错）。
	if err := g.AddDataset("", nil, 5); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("add empty: %v", err)
	}
	if _, err := g.Deprecate("", 100, 5); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("deprecate empty: %v", err)
	}
	if err := g.Advance("", 5); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("advance empty: %v", err)
	}
	if _, err := g.Access("", "d", 5); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("access empty consumer: %v", err)
	}
	if err := g.Extend("d", "c1", 0, 5); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("extend nonpositive: %v", err)
	}

	// 时钟回退先于不存在/阶段/自身错误。
	if _, err := g.Deprecate("nope", 100, 5); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clockback vs notfound: %v", err)
	}
	if err := g.Advance("nope", 5); !errors.Is(err, ErrClockBack) {
		t.Fatalf("advance clockback: %v", err)
	}

	// 不存在先于阶段错误。
	if _, err := g.Deprecate("nope", 100, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notfound deprecate: %v", err)
	}
	if err := g.Undeprecate("nope", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notfound undeprecate: %v", err)
	}

	// 阶段错误先于操作自身错误。
	_, _ = g.Deprecate("d", 100, 10)
	if _, err := g.Deprecate("d", 99, 10); !errors.Is(err, ErrPhase) {
		t.Fatalf("deprecate again short notice should be ErrPhase: %v", err)
	}
	if err := g.Undeprecate("d", 10); err != nil {
		t.Fatalf("undeprecate: %v", err)
	}
	// Active 上 Advance 报 ErrPhase（而非其它）。
	if err := g.Advance("d", 10); !errors.Is(err, ErrPhase) {
		t.Fatalf("advance active: %v", err)
	}

	// Retired 上 Access 报 ErrRetired（专用，不归 ErrPhase）；Retired 上 Advance 报 ErrPhase。
	_, _ = g.Deprecate("d", 100, 11)
	must(t, g.Advance("d", 51))
	must(t, g.Advance("d", 111))
	if _, err := g.Access("c1", "d", 111); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired access: %v", err)
	}
	if err := g.Advance("d", 111); !errors.Is(err, ErrPhase) {
		t.Fatalf("advance retired: %v", err)
	}
	// Extend 的自身错误次序 ErrNotConsumer > ErrExtendLimit > ErrTooLong 在
	// TestExtendMovesBothTimes 与 TestUndeprecateKeepsExtendCounters 中覆盖。
}

func TestRejectedOpsDoNotMoveClock(t *testing.T) {
	g, _ := New(exampleCfg())
	must(t, g.AddDataset("d", nil, 0))
	// 被拒操作不推进时钟：t=100 短通知被拒后，t=50 的合法调用仍应被接受。
	if _, err := g.Deprecate("d", 10, 100); !errors.Is(err, ErrNoticeTooShort) {
		t.Fatalf("short notice: %v", err)
	}
	if _, err := g.Deprecate("d", 100, 50); err != nil {
		t.Fatalf("clock moved despite rejection: %v", err)
	}
	must(t, g.Advance("d", 90)) // brownStart=50+100-60=90
	// ErrBrownout 被拒（now=94 大于当前时钟 90），不应推进时钟。
	if _, err := g.Access("c1", "d", 94); !errors.Is(err, ErrBrownout) {
		t.Fatalf("brownout: %v", err)
	}
	// 若上面拒绝把时钟推进到 44，则 now=43 的调用先报 ErrClockBack；
	// 时钟未推进时（lastNow=40）应只报 ErrBrownout。
	if _, err := g.Access("c1", "d", 93); !errors.Is(err, ErrBrownout) {
		t.Fatalf("brownout rejection moved the clock, got: %v", err)
	}
}

func TestScannedBoundAtAdvance(t *testing.T) {
	// 100 / 10000 僵尸消费者两档：scanned <= 近期(lastAccess>now-Q)消费者数 + 1。
	for _, stale := range []int{100, 10000} {
		g, _ := New(exampleCfg())
		must(t, g.AddDataset("d", nil, 0))
		_, _ = g.Deprecate("d", 100, 0)
		must(t, g.Advance("d", 40))
		// 早期大量消费者在 45 成功（周期0 的窗口外）。
		for i := 0; i < stale; i++ {
			if _, err := g.Access("z"+pad(i), "d", 45); err != nil {
				t.Fatal(err)
			}
		}
		// 两个近期消费者在 95 成功。
		if _, err := g.Access("live1", "d", 95); err != nil {
			t.Fatal(err)
		}
		if _, err := g.Access("live2", "d", 95); err != nil {
			t.Fatal(err)
		}
		err := g.Advance("d", 100)
		if !errors.Is(err, ErrConsumers) {
			t.Fatalf("stale=%d: %v", stale, err)
		}
		if items := ErrorItems(err); len(items) != 2 {
			t.Fatalf("stale=%d items=%v", stale, items)
		}
		if n := g.Scanned("d"); n > 3 {
			t.Fatalf("stale=%d scanned=%d, want <= 3", stale, n)
		}
	}
}

func pad(i int) string {
	s := ""
	for i >= 0 {
		s = string(rune('a'+i%26)) + s
		i = i/26 - 1
	}
	return s
}
