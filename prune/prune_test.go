package prune

import (
	"reflect"
	"testing"

	"ontology/part"
)

// 第三节五个分区。
var baseRows = []struct {
	id                 string
	lo, hi, minv, maxv int64
}{
	{"P0", 0, 10, 10, 20},
	{"P1", 10, 20, 30, 40},
	{"P2", 20, 30, 50, 60},
	{"P3", 30, 40, 15, 25},
	{"P4", 40, 50, 60, 80},
}

func newBaseTable(t *testing.T) *Table {
	t.Helper()
	tb := NewTable()
	for _, r := range baseRows {
		p, err := part.New(r.id, r.lo, r.hi, r.minv, r.maxv)
		if err != nil {
			t.Fatalf("part.New: %v", err)
		}
		if err := tb.Add(p); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	return tb
}

// bruteForce 不做二分，逐分区按两维相交求扫描集，作为 exactness 参照。
func bruteForce(tb *Table, q Predicate) []string {
	want := []string{}
	for _, p := range tb.parts {
		if !p.StaticPrune(q.Plo, q.Phi) && !p.DynamicPrune(q.Vlo, q.Vhi) {
			want = append(want, p.ID)
		}
	}
	return want
}

func reversedOrder(tb *Table, q Predicate) []string {
	got := []string{}
	for _, p := range tb.parts { // 先动态后静态
		if !p.DynamicPrune(q.Vlo, q.Vhi) && !p.StaticPrune(q.Plo, q.Phi) {
			got = append(got, p.ID)
		}
	}
	return got
}

// TestExact：剪枝最大化——扫描集 == 两维都相交的全部分区，裁剪集为其补集。
func TestExact(t *testing.T) {
	tb := newBaseTable(t)
	// 覆盖：题干谓词、恰好相接、单分区、空交集、全表等边界。
	cases := []struct {
		name               string
		Plo, Phi, Vlo, Vhi int64
	}{
		{"given", 20, 50, 30, 60},
		{"touch-Plo", 10, 20, 0, 100},
		{"touch-Phi", 20, 40, 0, 100},
		{"single-P2", 20, 30, 50, 60},
		{"v-touch", 0, 100, 20, 30},
		{"v-touch-hi", 0, 100, 40, 50},
		{"all-scan", 0, 50, 0, 100},
		{"gap", 50, 60, 0, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := Predicate{Plo: c.Plo, Phi: c.Phi, Vlo: c.Vlo, Vhi: c.Vhi}
			scan, pruned, err := tb.Query(q)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if want := bruteForce(tb, q); !reflect.DeepEqual(scan, want) {
				t.Fatalf("scan=%v want=%v", scan, want)
			}
			if len(scan)+len(pruned) != len(tb.parts) {
				t.Fatalf("scan+pruned != total: %d+%d", len(scan), len(pruned))
			}
		})
	}
}

// TestOrderIndependent：静态→动态 与 动态→静态 扫描集相同。
func TestOrderIndependent(t *testing.T) {
	tb := newBaseTable(t)
	cases := []Predicate{
		{Plo: 20, Phi: 50, Vlo: 30, Vhi: 60},
		{Plo: 10, Phi: 20, Vlo: 30, Vhi: 40},
		{Plo: 0, Phi: 50, Vlo: 0, Vhi: 100},
		{Plo: 25, Phi: 45, Vlo: 20, Vhi: 70},
	}
	for i, q := range cases {
		scan, _, err := tb.Query(q)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := reversedOrder(tb, q); !reflect.DeepEqual(scan, got) {
			t.Fatalf("case %d: static-first %v != dynamic-first %v", i, scan, got)
		}
	}
}

// TestCheckedSublinear：窄谓词下被检查分区数 = 常数 + 谓词内分区数，不随 N 线性增长。
func TestCheckedSublinear(t *testing.T) {
	ns := []int{100, 1000, 10000}
	prev := -1
	for _, n := range ns {
		tb := NewTable()
		for i := 0; i < n; i++ {
			p, err := part.New(idOf(i), int64(i*10), int64(i*10+10), 0, 1)
			if err != nil || tb.Add(p) != nil {
				t.Fatalf("n=%d build failed", n)
			}
		}
		scan, _, err := tb.Query(Predicate{Plo: 5, Phi: 15, Vlo: 0, Vhi: 2})
		if err != nil {
			t.Fatalf("n=%d Query: %v", n, err)
		}
		got := int(tb.checked.Load()) // 同包测试直读非导出计数器
		const slack = 2
		if got > len(scan)+slack {
			t.Fatalf("n=%d checked=%d but only %d partitions intersect", n, got, len(scan))
		}
		if prev >= 0 && got != prev {
			t.Fatalf("checked=%d at n=%d differs from %d: grows with N", got, n, prev)
		}
		prev = got
	}
}
