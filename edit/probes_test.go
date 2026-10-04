package edit

import (
	"fmt"
	"testing"

	"ontology/cue"
)

// buildBigTable 构造含 nd 个删除段与 ni 个插入点的合法表：
// 删除段放在前半区，插入点放在后半区的"安全格"之间，互不落在删除内部。
func buildBigTable(nd, ni int) Table {
	tbl := Table{}
	// 删除段：每段长 10，段间留 1000 间隙。
	for i := 0; i < nd; i++ {
		a0 := int64(i * 2000)
		tbl.Deletes = append(tbl.Deletes, Delete{A: a0, B: a0 + 10})
	}
	base := int64(nd*2000 + 1_000_000)
	for i := 0; i < ni; i++ {
		tbl.Inserts = append(tbl.Inserts, Insert{At: base + int64(i*100), Len: 1})
	}
	return tbl
}

func TestProbeBudget(t *testing.T) {
	for _, k := range []int{100, 10_000} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			nd, ni := k/2, k-k/2
			tbl := buildBigTable(nd, ni)
			c, err := Compile(tbl)
			if err != nil {
				t.Fatalf("k=%d compile: %v", k, err)
			}
			budget := int64(4 * ceilLog2(k+2))
			base := int64(nd*2000 + 1_000_000)
			// 单端映射：任取时间点，两个映射查询（fR 与 fL）的比较数
			// 均不超过整片预算 4*ceil(log2(k+2))。
			worst := int64(0)
			for _, x := range []int64{0, 1, 500_000, base, c.delA[nd-1] + 5, c.insAt[ni-1] + 1} {
				c.probes = 0
				_ = c.FR(x)
				if p := c.probes; p > worst {
					worst = p
				}
				c.probes = 0
				_ = c.FL(x)
				if p := c.probes; p > worst {
					worst = p
				}
			}
			if worst > budget {
				t.Fatalf("k=%d worst pair probes=%d > budget %d", k, worst, budget)
			}
			t.Logf("k=%d budget=4*ceil(log2(k+2))=%d, observed worst=%d", k, budget, worst)

			// 含大量内部插入点的字幕：Retime 内部对每片做断言；
			// 这里直接跑，若超预算 Retime 会 panic。
			cu := cue.Cue{
				Start: tbl.Inserts[0].At - 1,
				End:   tbl.Inserts[ni-1].At + 1,
				Text:  "z",
			}
			r := Retime(c, []cue.Cue{cu}, 1)
			if r.Splits != ni {
				t.Fatalf("k=%d splits=%d want %d", k, r.Splits, ni)
			}
		})
	}
}

func TestCeilLog2(t *testing.T) {
	cases := map[int]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 102: 7, 10002: 14}
	for n, want := range cases {
		if got := ceilLog2(n); got != want {
			t.Errorf("ceilLog2(%d)=%d want %d", n, got, want)
		}
	}
}
