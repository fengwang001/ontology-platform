package jrow

import "testing"

// 复杂度钉住：Locate 的查找次数是不随表规模增长的小常数。
func TestLocateProbesConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := make(map[string]int, m)
		r := make(map[string]int, m)
		for i := 0; i < m; i++ {
			k := string(rune('a'+i%26)) + string(rune('A'+i/26))
			l[k], r[k] = i, i*10
		}
		row := Locate(l, r, "aA")
		if !row.InL || !row.InR {
			t.Fatalf("m=%d: key 应同时存在", m)
		}
		if row.probes > 4 {
			t.Fatalf("m=%d: probes=%d 随规模线性增长", m, row.probes)
		}
	}
}

// 单 key 状态机的变更日志产出，表驱动。
func TestRowChanges(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		op   func(Row) (Row, []Change)
		want []Change
	}{
		{"左到右未到", Row{}, func(r Row) (Row, []Change) { return r.PutL("k", 1) }, nil},
		{"右到左已成行", Row{InL: true, LV: 1}, func(r Row) (Row, []Change) { return r.PutR("k", 2) },
			[]Change{{'+', "k", 1, 2}}},
		{"左值更新先撤后加", Row{InL: true, LV: 1, InR: true, RV: 2}, func(r Row) (Row, []Change) { return r.PutL("k", 9) },
			[]Change{{'-', "k", 1, 2}, {'+', "k", 9, 2}}},
		{"删左撤行", Row{InL: true, LV: 1, InR: true, RV: 2}, func(r Row) (Row, []Change) { return r.DelL("k") },
			[]Change{{'-', "k", 1, 2}}},
		{"删左未匹配无输出", Row{InL: true, LV: 1}, func(r Row) (Row, []Change) { return r.DelL("k") }, nil},
	}
	for _, c := range cases {
		_, got := c.op(c.row)
		if len(got) != len(c.want) {
			t.Fatalf("%s: 变更条数=%d 期望 %d", c.name, len(got), len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: 第%d条=%+v 期望 %+v", c.name, i, got[i], c.want[i])
			}
		}
	}
}
