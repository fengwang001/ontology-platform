package fww

import (
	"fmt"
	"math/rand"
	"testing"
)

// 共享用例：六步场景、多键交错、随机到达顺序（循环生成）。
func sequences() map[string][]Write {
	out := map[string][]Write{
		"six": {
			{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 10, Val: "c"},
			{Key: "K", Seq: 1, Val: "d"}, {Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"},
		},
		"multi": {
			{Key: "a", Seq: 2, Val: "v2"}, {Key: "b", Seq: 5, Val: "w5"}, {Key: "a", Seq: 1, Val: "v1"},
			{Key: "b", Seq: 3, Val: "w3"}, {Key: "a", Seq: 4, Val: "v4"}, {Key: "b", Seq: 1, Val: "w1"},
		},
	}
	rng := rand.New(rand.NewSource(7))
	base := []Write{
		{Key: "a", Seq: 1, Val: "v1"}, {Key: "a", Seq: 4, Val: "v4"}, {Key: "a", Seq: 2, Val: "v2"},
		{Key: "b", Seq: 3, Val: "w3"}, {Key: "b", Seq: 1, Val: "w1"}, {Key: "a", Seq: 6, Val: "v6"},
	}
	for p := 0; p < 6; p++ {
		sh := make([]Write, len(base))
		for i, j := range rng.Perm(len(base)) {
			sh[i] = base[j]
		}
		out[fmt.Sprintf("rand%d", p)] = sh
	}
	return out
}

// 不变量 2：changelog 的任意前缀自洽——撤回恰好等于当前值，同时至多一个生效值。
func TestChangelogConsistent(t *testing.T) {
	for name, ws := range sequences() {
		t.Run(name, func(t *testing.T) {
			m, cur := New(), map[string]Change{}
			for _, w := range ws {
				log, err := m.Feed([]Write{w})
				if err != nil {
					t.Fatalf("feed %+v: %v", w, err)
				}
				for _, c := range log {
					if c.Retract {
						old, ok := cur[c.Key]
						if !ok || old.Seq != c.Seq || old.Val != c.Val {
							t.Fatalf("retract %+v mismatches current %+v", c, old)
						}
						delete(cur, c.Key)
					} else {
						if _, dup := cur[c.Key]; dup {
							t.Fatalf("two effective values for %q", c.Key)
						}
						cur[c.Key] = c
					}
				}
			}
		})
	}
}

// 不变量 3：任一 Key 的生效 Seq 只减不增。
func TestSeqMonotone(t *testing.T) {
	for name, ws := range sequences() {
		t.Run(name, func(t *testing.T) {
			m, last := New(), map[string]int64{}
			for _, w := range ws {
				log, err := m.Feed([]Write{w})
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range log {
					if c.Retract {
						continue
					}
					if s, ok := last[c.Key]; ok && c.Seq >= s {
						t.Fatalf("effective seq increased: %d -> %d", s, c.Seq)
					}
					last[c.Key] = c.Seq
				}
			}
		})
	}
}

// 复杂度：最近一次写入检查的键个数是与 m 无关的小常数（白盒读非导出计数器）。
func TestCheckedKeysConst(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		m := New()
		ws := make([]Write, n)
		for i := range ws {
			ws[i] = Write{Key: fmt.Sprintf("k%d", i), Seq: 1, Val: "v"}
		}
		if _, err := m.Feed(ws); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Feed([]Write{{Key: "k0", Seq: 2, Val: "w"}}); err != nil {
			t.Fatal(err)
		}
		if m.checked > 2 {
			t.Fatalf("m=%d: checked %d keys, want <= 2", n, m.checked)
		}
	}
}
