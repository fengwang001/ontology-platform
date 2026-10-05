package inventory

import (
	"errors"
	"testing"
)

type grantStep struct {
	player string
	item   string // 为空表示发金币
	qty    int64
	want   error
}

func runGrants(t *testing.T, inv *Inv, steps []grantStep) {
	t.Helper()
	for i, st := range steps {
		var err error
		if st.item == "" {
			err = inv.GrantGold(st.player, st.qty)
		} else {
			err = inv.Grant(st.player, st.item, st.qty)
		}
		if !errors.Is(err, st.want) {
			t.Fatalf("step %d: got %v, want %v", i, err, st.want)
		}
		t.Logf("step %d: grant(%s %q %d) -> %v", i, st.player, st.item, st.qty, err)
	}
}

func TestGrant(t *testing.T) {
	cases := []struct {
		name      string
		cap       int64
		slots     int64
		steps     []grantStep
		wantKinds int64
		wantGold  int64
		wantHasB  bool
	}{
		{
			name: "发放物品与金币并登记玩家",
			cap:  100, slots: 2,
			steps: []grantStep{
				{"a", "x", 5, nil},
				{"a", "y", 1, nil},
				{"a", "", 40, nil},
			},
			wantKinds: 2, wantGold: 40,
		},
		{
			name: "同类物品累加不占用新格子",
			cap:  100, slots: 1,
			steps: []grantStep{
				{"a", "x", 5, nil},
				{"a", "x", 3, nil},
				{"a", "y", 1, ErrSlots},
			},
			wantKinds: 1,
		},
		{
			name: "发放被拒绝时不登记新玩家",
			cap:  100, slots: 1,
			steps: []grantStep{
				{"b", "", 200, ErrGoldCap},
				{"b", "y", 1, nil},
			},
			wantKinds: 0, wantHasB: true,
		},
		{
			name: "金币超过 CAP 被拒绝",
			cap:  100, slots: 2,
			steps: []grantStep{
				{"a", "", 60, nil},
				{"a", "", 41, ErrGoldCap},
				{"a", "", 40, nil},
			},
			wantGold: 100,
		},
		{
			name: "参数非法",
			cap:  100, slots: 2,
			steps: []grantStep{
				{"", "x", 1, ErrInvalid},
				{"a", "", -1, ErrInvalid},
				{"a", "x", -5, ErrInvalid},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := New(tc.cap, tc.slots)
			runGrants(t, inv, tc.steps)
			if got := inv.Kinds("a"); got != tc.wantKinds {
				t.Fatalf("kinds(a) = %d, want %d", got, tc.wantKinds)
			}
			if got := inv.Gold("a"); got != tc.wantGold {
				t.Fatalf("gold(a) = %d, want %d", got, tc.wantGold)
			}
			if got := inv.Has("b"); got != tc.wantHasB {
				t.Fatalf("has(b) = %v, want %v", got, tc.wantHasB)
			}
		})
	}
}

func TestKindsAfterDeductThenAdd(t *testing.T) {
	inv := New(1000, 2)
	runGrants(t, inv, []grantStep{
		{"a", "x", 2, nil},
		{"a", "y", 1, nil},
	})
	cases := []struct {
		name   string
		deltas map[string]int64
		want   int64
	}{
		{"付出全部 x 收到 z：y,z 两种", map[string]int64{"x": -2, "z": 1}, 2},
		{"只付出部分 x：x,y,z 三种", map[string]int64{"x": -1, "z": 1}, 3},
		{"付出 x 又收到 x：种类不变", map[string]int64{"x": -2, "z": 1, "y": 0}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inv.KindsAfter("a", tc.deltas); got != tc.want {
				t.Fatalf("KindsAfter = %d, want %d", got, tc.want)
			}
			t.Logf("deltas=%v -> kinds %d", tc.deltas, tc.want)
		})
	}
	if got := inv.Kinds("a"); got != 2 {
		t.Fatalf("KindsAfter 不应修改账本, kinds = %d", got)
	}
}

func TestApplyItemsAndGold(t *testing.T) {
	inv := New(1000, 3)
	runGrants(t, inv, []grantStep{
		{"a", "x", 5, nil},
		{"a", "", 100, nil},
	})
	inv.ApplyItems("a", map[string]int64{"x": -5, "y": 2})
	inv.ApplyGold("a", -30)
	if inv.Holding("a", "x") != 0 {
		t.Fatalf("x should be gone")
	}
	if inv.Holding("a", "y") != 2 || inv.Kinds("a") != 1 {
		t.Fatalf("y = %d, kinds = %d", inv.Holding("a", "y"), inv.Kinds("a"))
	}
	if inv.Gold("a") != 70 {
		t.Fatalf("gold = %d, want 70", inv.Gold("a"))
	}
}
