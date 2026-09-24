package ljoin

import (
	"fmt"
	"testing"

	"ontology/jstate"
)

// 证明按连接键定位而非整表扫描：插入 lz(z)、rz(z) 的检查行数不随 m 增长。
func TestChecksBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			p := New(jstate.New(), 10*m+10)
			for i := 0; i < m; i++ {
				k := fmt.Sprintf("k%06d", i)
				if _, err := p.ApplyOne(Change{L, Ins, "l" + k, k}); err != nil {
					t.Fatal(err)
				}
				if _, err := p.ApplyOne(Change{R, Ins, "r" + k, k}); err != nil {
					t.Fatal(err)
				}
			}
			for _, c := range []Change{{L, Ins, "lz", "z"}, {R, Ins, "rz", "z"}} {
				outs, err := p.ApplyOne(c)
				if err != nil {
					t.Fatal(err)
				}
				if got, lim := p.checked, len(outs)+2; got > lim {
					t.Fatalf("%v 检查行数 %d 超过上限 %d（输出 %d 条）", c, got, lim, len(outs))
				}
			}
		})
	}
}

func nineSteps() []Change {
	return []Change{{R, Ins, "r1", "x"}, {L, Ins, "l1", "x"}, {L, Ins, "l2", "y"},
		{L, Ins, "l3", "y"}, {R, Ins, "r2", "y"}, {R, Ins, "r3", "y"},
		{R, Del, "r2", ""}, {R, Del, "r3", ""}, {R, Del, "r1", ""}}
}

// 与 NOTES.md 九行表逐步对拍（含输出顺序）。
func TestNineSteps(t *testing.T) {
	want := [][]Out{
		{}, {{true, "l1", "r1"}}, {{true, "l2", ""}}, {{true, "l3", ""}},
		{{false, "l2", ""}, {true, "l2", "r2"}, {false, "l3", ""}, {true, "l3", "r2"}},
		{{true, "l2", "r3"}, {true, "l3", "r3"}},
		{{false, "l2", "r2"}, {false, "l3", "r2"}},
		{{false, "l2", "r3"}, {true, "l2", ""}, {false, "l3", "r3"}, {true, "l3", ""}},
		{{false, "l1", "r1"}, {true, "l1", ""}},
	}
	p := New(jstate.New(), 100)
	for i, c := range nineSteps() {
		outs, err := p.ApplyOne(c)
		if err != nil {
			t.Fatalf("第 %d 步: %v", i+1, err)
		}
		if len(outs) != len(want[i]) {
			t.Fatalf("第 %d 步: got %v want %v", i+1, outs, want[i])
		}
		for k := range outs {
			if outs[k] != want[i][k] {
				t.Fatalf("第 %d 步第 %d 条: got %v want %v", i+1, k+1, outs[k], want[i][k])
			}
		}
	}
}

// prefixes 核验变更日志每个前缀：计数 0/1、撤回须存在、NULL 行互斥。
func prefixes(t *testing.T, outs []Out) {
	t.Helper()
	type row struct{ l, r string }
	cnt := map[row]int{}
	null, match := map[string]int{}, map[string]int{}
	for i, o := range outs {
		r, d := row{o.LID, o.RID}, 1
		if !o.Plus {
			d = -1
		}
		if cnt[r]+d < 0 || cnt[r]+d > 1 {
			t.Fatalf("前缀 %d: 行 %v 计数越界", i, r)
		}
		cnt[r] += d
		if o.RID == "" {
			null[o.LID] += d
		} else {
			match[o.LID] += d
		}
		if null[o.LID] > 0 && match[o.LID] > 0 {
			t.Fatalf("前缀 %d: %s NULL 行与匹配行并存", i, o.LID)
		}
	}
}

// 九步序列累积日志的逐前缀核验（不变量 2、3）。
func TestLogPrefixConsistent(t *testing.T) {
	p := New(jstate.New(), 100)
	var log []Out
	for _, c := range nineSteps() {
		outs, err := p.ApplyOne(c)
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, outs...)
		prefixes(t, log)
	}
}
