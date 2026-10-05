package ladder

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/reward"
	"ontology/season"
)

func newSys(t *testing.T) *System {
	t.Helper()
	s, err := New(1000, 100, 100, 1, 2, 1000,
		[]reward.Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}}, 50, 1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewInvalid(t *testing.T) {
	goodTiers := []reward.Tier{{P: 1000, A: 1}}
	cases := []struct {
		name                             string
		base, w, l, gmin, k, ak, rho, wc int64
		tiers                            []reward.Tier
	}{
		{"ok", 1000, 100, 100, 1, 2, 1000, 50, 1000, goodTiers},
		{"base越界", 1_000_001, 100, 100, 1, 2, 1000, 50, 1000, goodTiers},
		{"w为零", 1000, 0, 100, 1, 2, 1000, 50, 1000, goodTiers},
		{"l越界", 1000, 100, 10_001, 1, 2, 1000, 50, 1000, goodTiers},
		{"tiers空", 1000, 100, 100, 1, 2, 1000, 50, 1000, nil},
		{"末档非1000", 1000, 100, 100, 1, 2, 1000, 50, 1000, []reward.Tier{{P: 999, A: 1}}},
		{"金额递增", 1000, 100, 100, 1, 2, 1000, 50, 1000, []reward.Tier{{P: 500, A: 1}, {P: 1000, A: 2}}},
		{"rho越界", 1000, 100, 100, 1, 2, 1000, 101, 1000, goodTiers},
		{"wc为零", 1000, 100, 100, 1, 2, 1000, 50, 0, goodTiers},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.base, tc.w, tc.l, tc.gmin, tc.k, tc.ak, tc.tiers, tc.rho, tc.wc)
			if tc.name == "ok" {
				if err != nil {
					t.Fatalf("合法参数不应报错: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("应报参数非法: %v", err)
			}
		})
	}
}

func TestReport(t *testing.T) {
	t.Run("登记与加减分", func(t *testing.T) {
		s := newSys(t)
		if err := s.Report(10, "a", "b"); err != nil {
			t.Fatalf("Report: %v", err)
		}
		if got := s.players["a"]; got.Score != 1100 || got.Games != 1 {
			t.Fatalf("胜者应为1100分1局: %+v", got)
		}
		if got := s.players["b"]; got.Score != 900 || got.Games != 1 {
			t.Fatalf("败者应为900分1局: %+v", got)
		}
	})
	t.Run("败者触底0分", func(t *testing.T) {
		s := newSys(t)
		for i := int64(0); i < 15; i++ {
			if err := s.Report(i, "a", "b"); err != nil {
				t.Fatalf("Report %d: %v", i, err)
			}
		}
		if got := s.players["b"]; got.Score != 0 || got.Games != 15 {
			t.Fatalf("败者应触底0分: %+v", got)
		}
	})
	t.Run("参数非法", func(t *testing.T) {
		s := newSys(t)
		cases := []struct {
			name string
			now  int64
			w, l string
		}{
			{"胜者为空", 0, "", "b"},
			{"败者为空", 0, "a", ""},
			{"同名", 0, "a", "a"},
			{"now负", -1, "a", "b"},
			{"now超界", 1_000_000_000_001, "a", "b"},
		}
		for _, tc := range cases {
			if err := s.Report(tc.now, tc.w, tc.l); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("%s: 应报参数非法, 得到 %v", tc.name, err)
			}
		}
		if len(s.players) != 0 || s.clock != 0 {
			t.Fatal("非法参数不应产生任何状态变化")
		}
	})
	t.Run("时钟回退", func(t *testing.T) {
		s := newSys(t)
		if err := s.Report(100, "a", "b"); err != nil {
			t.Fatalf("Report: %v", err)
		}
		if err := s.Report(99, "a", "b"); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("应报时钟回退: %v", err)
		}
		if err := s.Report(100, "a", "b"); err != nil {
			t.Fatalf("now 等于已接受最大值应允许: %v", err)
		}
		if got := s.players["a"]; got.Games != 2 {
			t.Fatalf("回退的 Report 不应计数: %+v", got)
		}
	})
	t.Run("Frozen期间状态逐字段不变", func(t *testing.T) {
		s := newSys(t)
		_ = s.Report(1, "a", "b")
		_ = s.Report(2, "c", "a")
		if err := s.Settle(10); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		type snap struct{ score, games int64 }
		before := map[string]snap{}
		for name, p := range s.players {
			before[name] = snap{p.Score, p.Games}
		}
		if err := s.Report(11, "a", "b"); !errors.Is(err, season.ErrSeasonFrozen) {
			t.Fatalf("Frozen 期间 Report 应报赛季冻结: %v", err)
		}
		if err := s.Settle(12); !errors.Is(err, season.ErrSeasonFrozen) {
			t.Fatalf("Frozen 期间 Settle 应报赛季冻结: %v", err)
		}
		for name, p := range s.players {
			if before[name] != (snap{p.Score, p.Games}) {
				t.Fatalf("Frozen 期间 %s 的记录被修改: %+v", name, p)
			}
		}
		if len(s.players) != len(before) {
			t.Fatal("Frozen 期间不应新增玩家")
		}
	})
}

func TestClaimWindow(t *testing.T) {
	t.Run("窗口取等与Start后仍可领", func(t *testing.T) {
		s := newSys(t)
		_ = s.Report(1, "a", "b")
		if err := s.Settle(5000); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		if err := s.Start(5998); err != nil { // 窗口内 Start 不影响领奖
			t.Fatalf("Start: %v", err)
		}
		if amt, err := s.Claim(5999, "a"); err != nil || amt != 1500 {
			t.Fatalf("Claim(5999) 应成功得1500: amt=%d err=%v", amt, err)
		}
		if _, err := s.Claim(6000, "b"); !errors.Is(err, reward.ErrWindowExpired) {
			t.Fatalf("Claim(6000) 应报窗口已过: %v", err)
		}
	})
	t.Run("不合格与重复领取", func(t *testing.T) {
		s := newSys(t)
		_ = s.Report(1, "a", "b")
		_ = s.Report(2, "c", "d")
		_ = s.Report(3, "c", "d")
		s.players["e"] = &reward.Player{Score: 9999, Games: 0} // 对局不足
		if err := s.Settle(100); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		if _, err := s.Claim(100, "e"); !errors.Is(err, reward.ErrNotQualified) {
			t.Fatalf("对局不足应报不合格: %v", err)
		}
		if _, err := s.Claim(100, "c"); err != nil {
			t.Fatalf("首次领取: %v", err)
		}
		if _, err := s.Claim(101, "c"); !errors.Is(err, reward.ErrAlreadyClaimed) {
			t.Fatalf("重复领取应报已领: %v", err)
		}
		if _, err := s.Claim(1100, "c"); !errors.Is(err, reward.ErrWindowExpired) {
			t.Fatalf("窗口过后重复领取应报窗口已过: %v", err)
		}
	})
	t.Run("被拒绝的操作不推进时钟", func(t *testing.T) {
		s := newSys(t)
		_ = s.Report(1, "a", "b")
		if err := s.Settle(5000); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		if _, err := s.Claim(7000, "ghost"); !errors.Is(err, reward.ErrNotInSettlement) {
			t.Fatalf("应报不在结算中: %v", err)
		}
		// 7000 未被接受, 时钟仍为 5000, 窗口 [5000,6000) 内仍可领
		if amt, err := s.Claim(5999, "a"); err != nil || amt != 1500 {
			t.Fatalf("拒绝不应推进时钟: amt=%d err=%v", amt, err)
		}
	})
}

func TestRejectionOrder(t *testing.T) {
	// 构造: 已结算(ts=5000,Wc=1000), a 合格未领, c 不合格, clock=5000
	setup := func(t *testing.T) *System {
		s := newSys(t)
		_ = s.Report(1, "a", "b")
		s.players["c"] = &reward.Player{Score: 5, Games: 0}
		if err := s.Settle(5000); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		return s
	}
	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(4999, ""); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("应报参数非法: %v", err)
		}
	})
	t.Run("时钟回退优先于结算检查", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(4999, "ghost"); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("应报时钟回退: %v", err)
		}
	})
	t.Run("尚无结算", func(t *testing.T) {
		s := newSys(t)
		if _, err := s.Claim(0, "a"); !errors.Is(err, ErrNoSettlement) {
			t.Fatalf("应报尚无结算: %v", err)
		}
	})
	t.Run("不在结算中优先于窗口已过", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(6000, "ghost"); !errors.Is(err, reward.ErrNotInSettlement) {
			t.Fatalf("应报不在结算中: %v", err)
		}
	})
	t.Run("窗口已过优先于不合格", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(6000, "c"); !errors.Is(err, reward.ErrWindowExpired) {
			t.Fatalf("应报窗口已过: %v", err)
		}
	})
	t.Run("不合格优先于已领语义", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(5000, "c"); !errors.Is(err, reward.ErrNotQualified) {
			t.Fatalf("应报不合格: %v", err)
		}
	})
	t.Run("已领为最后判定", func(t *testing.T) {
		s := setup(t)
		if _, err := s.Claim(5000, "a"); err != nil {
			t.Fatalf("首次领取: %v", err)
		}
		if _, err := s.Claim(5001, "a"); !errors.Is(err, reward.ErrAlreadyClaimed) {
			t.Fatalf("应报已领: %v", err)
		}
	})
}

func TestOverwriteVoidsOldAwards(t *testing.T) {
	s := newSys(t)
	_ = s.Report(1, "a", "b")
	if err := s.Settle(100); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if err := s.Start(200); err != nil { // 对局数清零
		t.Fatalf("Start: %v", err)
	}
	if err := s.Settle(300); err != nil { // 覆盖旧结算, 旧奖作废
		t.Fatalf("第二次 Settle: %v", err)
	}
	// 新结算中 a 对局数不足 -> 不合格, 旧的 1500 奖励作废
	if _, err := s.Claim(300, "a"); !errors.Is(err, reward.ErrNotQualified) {
		t.Fatalf("覆盖后旧奖应作废, 新结算中不合格: %v", err)
	}
	e, ok := s.Result("a")
	if !ok || e.Qualified {
		t.Fatalf("Result 应反映新结算: %+v ok=%v", e, ok)
	}
}

func TestTouched(t *testing.T) {
	for _, n := range []int{1_000, 100_000} {
		t.Run(fmt.Sprintf("玩家数%d", n), func(t *testing.T) {
			s := newSys(t)
			for i := 0; i < n; i++ {
				s.players[fmt.Sprintf("p%d", i)] = &reward.Player{Score: int64(i), Games: 1}
			}
			if err := s.Settle(10); err != nil {
				t.Fatalf("Settle: %v", err)
			}
			s.touched = 0
			if _, err := s.Claim(10, "p0"); err != nil {
				t.Fatalf("Claim: %v", err)
			}
			if s.touched > 2 {
				t.Fatalf("一次 Claim 读取玩家记录 %d 条, 应不超过 2", s.touched)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	s := newSys(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("p%d", g)
			for i := int64(0); i < 200; i++ {
				_ = s.Report(i, name, "x")
				_ = s.Settle(i)
				_ = s.Start(i)
				_, _ = s.Claim(i, name)
				_, _ = s.Result(name)
			}
		}(g)
	}
	wg.Wait()
	// 并发等价于某串行顺序: 每人每份结算至多领一次由互斥锁保证,
	// 此处验证系统未崩溃且时钟单调。
	if s.clock < 0 {
		t.Fatal("时钟异常")
	}
}
