package reward

import (
	"errors"
	"fmt"
	"testing"
)

func baseConfig() Config {
	return Config{
		Base: 1000, W: 100, L: 100, GMin: 1,
		K: 2, AK: 1000,
		Tiers: []Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}},
		Rho:   50, Wc: 1000,
	}
}

func TestValidate(t *testing.T) {
	good := baseConfig()
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"ok", func(c *Config) {}, false},
		{"base负", func(c *Config) { c.Base = -1 }, true},
		{"base超界", func(c *Config) { c.Base = 1_000_001 }, true},
		{"w零", func(c *Config) { c.W = 0 }, true},
		{"w超界", func(c *Config) { c.W = 10_001 }, true},
		{"l零", func(c *Config) { c.L = 0 }, true},
		{"gmin负", func(c *Config) { c.GMin = -1 }, true},
		{"gmin超界", func(c *Config) { c.GMin = 10_001 }, true},
		{"k负", func(c *Config) { c.K = -1 }, true},
		{"k超界", func(c *Config) { c.K = 1_000_001 }, true},
		{"ak负", func(c *Config) { c.AK = -1 }, true},
		{"ak超界", func(c *Config) { c.AK = 1_000_000_001 }, true},
		{"tiers空", func(c *Config) { c.Tiers = nil }, true},
		{"tiers超8档", func(c *Config) {
			c.Tiers = []Tier{{100, 9}, {200, 8}, {300, 7}, {400, 6}, {500, 5}, {600, 4}, {700, 3}, {800, 2}, {1000, 1}}
		}, true},
		{"p未递增", func(c *Config) { c.Tiers = []Tier{{500, 2}, {500, 1}, {1000, 0}} }, true},
		{"p倒序", func(c *Config) { c.Tiers = []Tier{{600, 2}, {500, 1}, {1000, 0}} }, true},
		{"p超1000", func(c *Config) { c.Tiers = []Tier{{1001, 0}} }, true},
		{"末档非1000", func(c *Config) { c.Tiers = []Tier{{100, 1}, {999, 0}} }, true},
		{"金额负", func(c *Config) { c.Tiers = []Tier{{1000, -1}} }, true},
		{"金额超界", func(c *Config) { c.Tiers = []Tier{{1000, 1_000_000_001}} }, true},
		{"金额递增", func(c *Config) { c.Tiers = []Tier{{500, 1}, {1000, 2}} }, true},
		{"rho负", func(c *Config) { c.Rho = -1 }, true},
		{"rho超100", func(c *Config) { c.Rho = 101 }, true},
		{"wc零", func(c *Config) { c.Wc = 0 }, true},
		{"wc超界", func(c *Config) { c.Wc = 10_000_000_001 }, true},
		{"边界全取极限合法", func(c *Config) {
			c.Base, c.W, c.L, c.GMin, c.K, c.AK, c.Rho, c.Wc = 1_000_000, 10_000, 10_000, 10_000, 1_000_000, 1_000_000_000, 100, 10_000_000_000
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			cfg.Tiers = append([]Tier(nil), good.Tiers...)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate()=%v, wantErr=%v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("错误应为 ErrInvalidParam, 得到 %v", err)
			}
		})
	}
}

func TestTierOf(t *testing.T) {
	tiers := []Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}}
	cases := []struct {
		name     string
		rank, n  int64
		tiers    []Tier
		wantTier int
	}{
		{"r1例外_N1", 1, 1, tiers, 0},       // 1*1000 > 1*1000 之外各档，无例外则落末档
		{"r1例外_N大", 1, 100_000, tiers, 0}, // 1*1000 <= 100000*100 本就入第0档
		{"取等入档_N6_r3", 3, 6, tiers, 1},    // 3000 <= 6*500=3000 取等
		{"越界_N6_r4", 4, 6, tiers, 2},      // 4000 > 3000
		{"N7_r3", 3, 7, tiers, 1},         // 3000 <= 3500
		{"N7_r2", 2, 7, tiers, 1},         // 2000 > 700, <= 3500
		{"N7_r7", 7, 7, tiers, 2},         // 7000 <= 7000
		{"N10_r2", 2, 10, tiers, 1},       // 2000 > 1000, <= 5000
		{"单档", 5, 5, []Tier{{1000, 7}}, 0},
		{"末档兜底", 100, 100, tiers, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierOf(tc.rank, tc.n, tc.tiers); got != tc.wantTier {
				t.Fatalf("TierOf(%d,%d)=%d, 期望 %d", tc.rank, tc.n, got, tc.wantTier)
			}
		})
	}
}

// mkPlayers 由分数表构造玩家（games 全部设为 g）。
func mkPlayers(scores map[string]int64, g int64) map[string]*Player {
	m := make(map[string]*Player, len(scores))
	for name, s := range scores {
		m[name] = &Player{Score: s, Games: g}
	}
	return m
}

func TestCompute(t *testing.T) {
	t.Run("规范示例_N7", func(t *testing.T) {
		players := mkPlayers(map[string]int64{
			"a": 1300, "b": 1300, "c": 1200,
			"d": 1100, "e": 1100, "f": 1100, "g": 1000,
		}, 1)
		st := Compute(players, baseConfig(), 5000)
		want := map[string]Entry{
			"a": {Rank: 1, Tier: 0, Amount: 1500, Qualified: true},
			"b": {Rank: 1, Tier: 0, Amount: 1500, Qualified: true},
			"c": {Rank: 3, Tier: 1, Amount: 200, Qualified: true},
			"d": {Rank: 4, Tier: 2, Amount: 50, Qualified: true},
			"e": {Rank: 4, Tier: 2, Amount: 50, Qualified: true},
			"f": {Rank: 4, Tier: 2, Amount: 50, Qualified: true},
			"g": {Rank: 7, Tier: 2, Amount: 50, Qualified: true},
		}
		for name, we := range want {
			ge, ok := st.Result(name)
			if !ok || ge != we {
				t.Fatalf("%s: 得到 %+v(ok=%v), 期望 %+v", name, ge, ok, we)
			}
		}
	})
	t.Run("取等变体_N6", func(t *testing.T) {
		players := mkPlayers(map[string]int64{
			"a": 1300, "b": 1300, "c": 1200,
			"d": 1100, "e": 1100, "f": 1100,
		}, 1)
		st := Compute(players, baseConfig(), 5000)
		if e, _ := st.Result("c"); e.Rank != 3 || e.Tier != 1 || e.Amount != 200 {
			t.Fatalf("r=3 应取等入第2档: %+v", e)
		}
		if e, _ := st.Result("d"); e.Rank != 4 || e.Tier != 2 || e.Amount != 50 {
			t.Fatalf("r=4 应入第3档: %+v", e)
		}
	})
	t.Run("并列跨档超额照发", func(t *testing.T) {
		cfg := baseConfig()
		cfg.K, cfg.AK = 0, 0
		cfg.Tiers = []Tier{{500, 300}, {1000, 100}}
		players := mkPlayers(map[string]int64{"a": 9, "b": 5, "c": 5, "d": 5}, 1)
		st := Compute(players, cfg, 0)
		// N=4, r=2: 2000 <= 4*500=2000 取等入第0档, 第0档名义名额 2 人实发 4 人
		for _, name := range []string{"a", "b", "c", "d"} {
			e, _ := st.Result(name)
			if e.Tier != 0 || e.Amount != 300 {
				t.Fatalf("%s 应入第0档得300: %+v", name, e)
			}
		}
		if e, _ := st.Result("b"); e.Rank != 2 {
			t.Fatalf("并列名次应为2: %+v", e)
		}
	})
	t.Run("不合格者不占名次", func(t *testing.T) {
		players := mkPlayers(map[string]int64{"a": 5000, "b": 100}, 1)
		players["a"].Games = 0 // 高分但不合格
		st := Compute(players, baseConfig(), 0)
		ea, ok := st.Result("a")
		if !ok || ea.Qualified || ea.Tier != -1 || ea.Rank != 0 {
			t.Fatalf("不合格者条目错误: %+v ok=%v", ea, ok)
		}
		eb, _ := st.Result("b")
		if eb.Rank != 1 || eb.Tier != 0 { // r=1 例外入第0档
			t.Fatalf("不合格者不应占名次: %+v", eb)
		}
	})
	t.Run("N0空结算", func(t *testing.T) {
		st := Compute(map[string]*Player{}, baseConfig(), 0)
		if len(st.Entries) != 0 {
			t.Fatalf("空玩家表应得空结算: %d", len(st.Entries))
		}
		players := mkPlayers(map[string]int64{"a": 100}, 0) // 全不合格
		st = Compute(players, baseConfig(), 0)
		if e, ok := st.Result("a"); !ok || e.Qualified {
			t.Fatalf("全不合格: %+v ok=%v", e, ok)
		}
	})
	t.Run("分高者奖励不低于分低者", func(t *testing.T) {
		players := mkPlayers(map[string]int64{
			"p1": 1300, "p2": 1250, "p3": 1200, "p4": 1150,
			"p5": 1100, "p6": 1050, "p7": 1000, "p8": 950,
		}, 3)
		st := Compute(players, baseConfig(), 0)
		prev := int64(-1)
		for i := 1; i <= 8; i++ {
			e, _ := st.Result(fmt.Sprintf("p%d", i))
			if prev >= 0 && e.Amount > prev {
				t.Fatalf("分低者奖励更高: p%d 得 %d > %d", i, e.Amount, prev)
			}
			prev = e.Amount
		}
	})
}

func TestSettlementClaim(t *testing.T) {
	newSt := func() *Settlement {
		players := mkPlayers(map[string]int64{"a": 1300, "b": 1200}, 1)
		players["c"] = &Player{Score: 5000, Games: 0} // 不合格
		return Compute(players, baseConfig(), 5000)   // ts=5000, Wc=1000
	}
	t.Run("窗口右开取等过期", func(t *testing.T) {
		st := newSt()
		if _, err := st.Claim("a", 5999, 1000); err != nil {
			t.Fatalf("5999 应在窗口内: %v", err)
		}
		if _, err := st.Claim("b", 6000, 1000); !errors.Is(err, ErrWindowExpired) {
			t.Fatalf("6000 应报窗口已过: %v", err)
		}
	})
	t.Run("拒绝次序_不在结算中优先于窗口", func(t *testing.T) {
		st := newSt()
		if _, err := st.Claim("ghost", 7000, 1000); !errors.Is(err, ErrNotInSettlement) {
			t.Fatalf("窗口过后未登记者仍应报不在结算中: %v", err)
		}
	})
	t.Run("不合格", func(t *testing.T) {
		st := newSt()
		if _, err := st.Claim("c", 5000, 1000); !errors.Is(err, ErrNotQualified) {
			t.Fatalf("应报不合格: %v", err)
		}
	})
	t.Run("重复领取与窗口优先于已领", func(t *testing.T) {
		st := newSt()
		if amt, err := st.Claim("a", 5000, 1000); err != nil || amt != 1500 {
			t.Fatalf("首次领取: amt=%d err=%v", amt, err)
		}
		if _, err := st.Claim("a", 5001, 1000); !errors.Is(err, ErrAlreadyClaimed) {
			t.Fatalf("窗口内重复应报已领: %v", err)
		}
		if _, err := st.Claim("a", 6000, 1000); !errors.Is(err, ErrWindowExpired) {
			t.Fatalf("窗口过后重复应报窗口已过: %v", err)
		}
	})
	t.Run("被拒绝的领取不改状态", func(t *testing.T) {
		st := newSt()
		_, _ = st.Claim("c", 5000, 1000)     // 不合格, 拒绝
		_, _ = st.Claim("ghost", 5000, 1000) // 不在结算中, 拒绝
		if amt, err := st.Claim("a", 5000, 1000); err != nil || amt != 1500 {
			t.Fatalf("拒绝不应影响后续领取: amt=%d err=%v", amt, err)
		}
	})
}
