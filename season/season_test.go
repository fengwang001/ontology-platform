package season

import (
	"errors"
	"testing"

	"ontology/reward"
)

func testCfg() reward.Config {
	return reward.Config{
		Base: 1000, W: 100, L: 100, GMin: 1,
		K: 2, AK: 1000,
		Tiers: []reward.Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}},
		Rho:   50, Wc: 1000,
	}
}

func TestStateMachine(t *testing.T) {
	cfg := testCfg()
	cases := []struct {
		name string
		ops  []string // "settle" / "start"
		want []error  // 逐步期望, nil 表成功
	}{
		{"开局不可Start", []string{"start"}, []error{ErrNotFrozen}},
		{"Settle后不可重复Settle", []string{"settle", "settle"}, []error{nil, ErrSeasonFrozen}},
		{"完整轮转", []string{"settle", "start", "settle"}, []error{nil, nil, nil}},
		{"Start后再Start报错", []string{"settle", "start", "start"}, []error{nil, nil, ErrNotFrozen}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Machine{}
			players := map[string]*reward.Player{"a": {Score: 1200, Games: 3}}
			for i, op := range tc.ops {
				var err error
				if op == "settle" {
					err = m.Settle(players, cfg, int64(1000+i))
				} else {
					err = m.Start(players, cfg)
				}
				if !errors.Is(err, tc.want[i]) {
					t.Fatalf("第%d步%s: err=%v, 期望 %v", i, op, err, tc.want[i])
				}
			}
		})
	}
}

func TestSoftReset(t *testing.T) {
	cases := []struct {
		name      string
		base, rho int64
		old, want int64
	}{
		{"正差_1301", 1000, 50, 1301, 1150},  // 1000+floor(150.5)
		{"负差向下取整_997", 1000, 50, 997, 998}, // 1000+floor(-1.5), 截断会得999
		{"零分", 1000, 50, 0, 500},
		{"恰为base", 1000, 50, 1000, 1000},
		{"rho0全部归base", 1000, 0, 7777, 1000},
		{"rho0负差", 1000, 0, 0, 1000},
		{"rho100不变", 1000, 100, 1301, 1301},
		{"rho100低分不变", 1000, 100, 997, 997},
		{"负差整除", 1000, 50, 900, 950},   // 1000+floor(-50)
		{"rho33负差", 500, 33, 499, 499}, // 500+floor(-0.33)=499
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Base, cfg.Rho = tc.base, tc.rho
			m := &Machine{Frozen: true}
			players := map[string]*reward.Player{"a": {Score: tc.old, Games: 7}}
			if err := m.Start(players, cfg); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if players["a"].Score != tc.want {
				t.Fatalf("软重置 %d -> %d, 期望 %d", tc.old, players["a"].Score, tc.want)
			}
			if players["a"].Games != 0 {
				t.Fatalf("对局数应清零, 得到 %d", players["a"].Games)
			}
			if m.Frozen {
				t.Fatal("Start 后应转入 Open")
			}
		})
	}
}

func TestSettleAndStartKeepSettlement(t *testing.T) {
	cfg := testCfg()
	m := &Machine{}
	players := map[string]*reward.Player{"a": {Score: 1200, Games: 3}}

	if err := m.Settle(players, cfg, 5000); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	first := m.Settlement
	if m.Ts != 5000 || !m.Frozen {
		t.Fatalf("Settle 后状态错误: ts=%d frozen=%v", m.Ts, m.Frozen)
	}
	// Start 不影响已生成的结算结果
	if err := m.Start(players, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.Settlement != first {
		t.Fatal("Start 不应改变结算结果")
	}
	// 下一次 Settle 覆盖旧结算
	if err := m.Settle(players, cfg, 9000); err != nil {
		t.Fatalf("第二次 Settle: %v", err)
	}
	if m.Settlement == first || m.Ts != 9000 {
		t.Fatal("第二次 Settle 应覆盖旧结算并更新 ts")
	}
}
