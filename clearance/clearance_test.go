package clearance

import (
	"errors"
	"fmt"
	"testing"

	"ontology/grant"
	"ontology/territory"
)

func setupExample(t *testing.T) (*grant.Registry, *Engine) {
	t.Helper()
	tr, err := territory.New(map[string][]string{
		"WORLD": {"EU", "AS"},
		"EU":    {"FR", "DE"},
		"AS":    {"JP", "KR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := grant.NewRegistry(tr)
	add := func(id, who, node string, excl []string, s, e int64, ex bool) {
		if err := reg.Add(0, id, "T", who, node, excl, s, e, ex); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	add("g1", "甲", "EU", []string{"FR"}, 100, 200, true)
	add("g2", "乙", "WORLD", []string{"DE"}, 150, 300, true)
	add("g4", "丙", "DE", nil, 200, 250, false)
	return reg, New(reg)
}

func TestCanPlayExample(t *testing.T) {
	_, e := setupExample(t)

	// 撤销 g1 截断到 160（模拟题目后半段先复现前半段）。
	// 直接新建一个与题目后半段一致的状态：
	tr, _ := territory.New(map[string][]string{
		"WORLD": {"EU", "AS"},
		"EU":    {"FR", "DE"},
		"AS":    {"JP", "KR"},
	})
	reg := grant.NewRegistry(tr)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(reg.Add(0, "g1", "T", "甲", "EU", []string{"FR"}, 100, 200, true))
	must(reg.Add(0, "g2", "T", "乙", "WORLD", []string{"DE"}, 150, 300, true))
	must(reg.Add(0, "g4", "T", "丙", "DE", nil, 200, 250, false))
	must(reg.Revoke(160, "g1"))
	eng := New(reg)

	cases := []struct {
		name    string
		who     string
		leaf    string
		at      int64
		verdict string
		exclID  string
	}{
		{"丙 DE 205 允许", "丙", "DE", 205, VerdictAllow, ""},
		{"丙 FR 205 被独占 g2", "丙", "FR", 205, VerdictExclusive, "g2"},
		{"丙 DE 260 无授权", "丙", "DE", 260, VerdictNoLicense, ""},
		{"甲 DE 160 无授权（恰等 end）", "甲", "DE", 160, VerdictNoLicense, ""},
		{"甲 DE 159 允许（截断窗内）", "甲", "DE", 159, VerdictAllow, ""},
		{"乙 DE 205 无授权（DE 被 g2 排除）", "乙", "DE", 205, VerdictNoLicense, ""},
		{"乙 JP 205 允许", "乙", "JP", 205, VerdictAllow, ""},
	}
	for _, c := range cases {
		d, err := eng.CanPlay("T", c.who, c.leaf, c.at)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d.Verdict != c.verdict || d.ExclusiveID != c.exclID {
			t.Errorf("%s: got %+v want %s/%s", c.name, d, c.verdict, c.exclID)
		}
	}

	// 未撤销状态下的独占判定（用 e）。
	d, err := e.CanPlay("T", "乙", "DE", 150)
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != VerdictExclusive || d.ExclusiveID != "g1" {
		t.Fatalf("乙 DE 150: %+v want exclusive g1", d)
	}
}

func TestHolders(t *testing.T) {
	reg, _ := setupExample(t)
	eng := New(reg)
	// FR @170：g2 覆盖 FR[150,300)；g1 排除了 FR。
	hs, err := eng.Holders("T", "FR", 170)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].ID != "g2" {
		t.Fatalf("FR@170 holders = %v want [g2]", hs)
	}
	// DE @210：g1[100,200) 已过，g4[200,250) 生效，g2 排除 DE。
	hs, _ = eng.Holders("T", "DE", 210)
	if len(hs) != 1 || hs[0].ID != "g4" {
		t.Fatalf("DE@210 holders = %v want [g4]", hs)
	}

	// Revoke(now=start) 整条删除后 Holders 为空。
	tr := reg.Tree()
	reg2 := grant.NewRegistry(tr)
	if err := reg2.Add(0, "g1", "T", "甲", "EU", []string{"FR"}, 100, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := reg2.Revoke(100, "g1"); err != nil {
		t.Fatal(err)
	}
	hs, _ = New(reg2).Holders("T", "DE", 150)
	if len(hs) != 0 {
		t.Fatalf("holders after delete = %v want empty", hs)
	}
}

func TestCanPlayErrors(t *testing.T) {
	_, eng := setupExample(t)
	if _, err := eng.CanPlay("T", "丙", "DE", -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid t: %v", err)
	}
	if _, err := eng.CanPlay("T", "丙", "MARS", 0); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := eng.CanPlay("T", "丙", "EU", 0); !errors.Is(err, ErrNotLeaf) {
		t.Fatalf("not leaf: %v", err)
	}
}

func TestConcurrentSafety(t *testing.T) {
	// 100 个不同 title 下各加独占授权（互不冲突），并发写入与读取不应触发 race，
	// 且同一 title 下他方独占互斥的不变量始终成立。
	tr, err := territory.New(map[string][]string{
		"WORLD": {"A", "B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := grant.NewRegistry(tr)
	eng := New(reg)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			title := fmt.Sprintf("T%d", i%20)
			id := fmt.Sprintf("g-%d", i)
			lic := fmt.Sprintf("lic-%d", i%3)
			_ = reg.Add(int64(i), id, title, lic, "A", nil, 0, 1000, true)
		}
	}()
	for i := 0; i < 200; i++ {
		_, _ = eng.CanPlay(fmt.Sprintf("T%d", i%20), "x", "A", int64(i%1000))
		_, _ = eng.Holders(fmt.Sprintf("T%d", i%20), "B", int64(i%1000))
	}
	<-done
	// 每个 title 若有独占生效，全部生效授权属于同一被授权方（不变量）。
	for k := 0; k < 20; k++ {
		hs, err := eng.Holders(fmt.Sprintf("T%d", k), "A", 500)
		if err != nil {
			t.Fatal(err)
		}
		who := ""
		for _, g := range hs {
			if !g.Exclusive {
				continue
			}
			if who == "" {
				who = g.Licensee
			} else if g.Licensee != who {
				t.Fatalf("invariant broken on T%d: exclusive licensees %s vs %s", k, who, g.Licensee)
			}
		}
	}
}
