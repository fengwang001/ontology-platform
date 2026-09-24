package rank

import (
	"math"
	"math/rand"
	"testing"
)

func TestSetOrder(t *testing.T) {
	cases := []struct {
		name string
		rows []Row
		want Row
	}{
		{"one", []Row{{"a", 5}}, Row{"a", 5}},
		{"ascending", []Row{{"a", 1}, {"b", 2}, {"c", 3}}, Row{"a", 1}},
		{"descending", []Row{{"c", 3}, {"b", 2}, {"a", 1}}, Row{"a", 1}},
		{"negative T", []Row{{"a", -9}, {"b", 0}, {"c", -10}}, Row{"c", -10}},
		{"tie by ID", []Row{{"p", 3}, {"m", 3}, {"a", 5}}, Row{"m", 3}},
		{"byte order", []Row{{"a9", 1}, {"a10", 1}}, Row{"a10", 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for _, r := range tc.rows {
				s.Insert(r)
			}
			got, ok := s.Min()
			if !ok || got != tc.want {
				t.Fatalf("Min = %v,%v want %v", got, ok, tc.want)
			}
			if s.Len() != len(tc.rows) {
				t.Fatalf("Len = %d want %d", s.Len(), len(tc.rows))
			}
		})
	}
}

func TestDelete(t *testing.T) {
	type op struct {
		del  bool
		row  Row
		want bool // Delete 的返回；插入场景忽略
		min  Row  // 操作后 Min
		has  bool
	}
	cases := []struct {
		name string
		ops  []op
	}{
		{"min removal", []op{
			{false, Row{"a", 1}, false, Row{"a", 1}, true},
			{false, Row{"b", 2}, false, Row{"a", 1}, true},
			{true, Row{"a", 1}, true, Row{"b", 2}, true},
			{true, Row{"b", 2}, true, Row{}, false},
		}},
		{"tie then delete", []op{
			{false, Row{"p", 3}, false, Row{"p", 3}, true},
			{false, Row{"m", 3}, false, Row{"m", 3}, true},
			{true, Row{"m", 3}, true, Row{"p", 3}, true},
		}},
		{"reinsert after delete", []op{
			{false, Row{"a", 5}, false, Row{"a", 5}, true},
			{true, Row{"a", 5}, true, Row{}, false},
			{false, Row{"a", 5}, false, Row{"a", 5}, true},
		}},
		{"delete missing", []op{
			{false, Row{"a", 1}, false, Row{"a", 1}, true},
			{true, Row{"x", 1}, false, Row{"a", 1}, true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for _, o := range tc.ops {
				if o.del {
					if got := s.Delete(o.row); got != o.want {
						t.Fatalf("Delete(%v)=%v want %v", o.row, got, o.want)
					}
				} else {
					s.Insert(o.row)
				}
				got, ok := s.Min()
				if ok != o.has || ok && got != o.min {
					t.Fatalf("after %v: Min=(%v,%v) want (%v,%v)", o, got, ok, o.min, o.has)
				}
			}
		})
	}
}

// TestCompareCount 钉住复杂度：插最大行、删当前首条各自的排序键比较次数
// 都不得随存活行数 m 线性增长，阈值 4*(floor(log2 m)+2)。
func TestCompareCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		bound := 4 * (int(math.Floor(math.Log2(float64(m)))) + 2)
		s, rng := New(), rand.New(rand.NewSource(int64(m)))
		var maxT int64
		for i := 0; i < m; i++ {
			tv := rng.Int63n(int64(m) * 4)
			if tv > maxT {
				maxT = tv
			}
			s.Insert(Row{ID: idFor(i), T: tv})
		}
		s.Insert(Row{ID: "zzz-max", T: maxT + 1})
		if s.cmp > bound {
			t.Fatalf("m=%d insert-max cmp=%d > %d", m, s.cmp, bound)
		}
		head, ok := s.Min()
		if !ok {
			t.Fatalf("m=%d empty set", m)
		}
		s.Delete(head)
		if s.cmp > bound {
			t.Fatalf("m=%d delete-min cmp=%d > %d", m, s.cmp, bound)
		}
		if s.Len() != m {
			t.Fatalf("m=%d Len=%d want %d", m, s.Len(), m)
		}
	}
}

func idFor(i int) string {
	const digits = "0123456789"
	b := make([]byte, 6)
	for j := 5; j >= 0; j-- {
		b[j] = digits[i%10]
		i /= 10
	}
	return "id" + string(b)
}
