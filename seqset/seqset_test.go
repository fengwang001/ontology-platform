package seqset

import (
	"math"
	"math/rand"
	"testing"
)

func bound(g int) int { return 2*int(math.Ceil(math.Log2(float64(g+2)))) + 4 }

// TestInsertContains 表驱动验证插入、合并、计数与区间查询。
func TestInsertContains(t *testing.T) {
	cases := []struct {
		name   string
		insert [][2]int64
		want   map[int64]bool
		count  int64
		segs   int
	}{
		{
			name:   "相邻自动合并",
			insert: [][2]int64{{1, 4}, {4, 7}, {7, 10}},
			want:   map[int64]bool{0: false, 1: true, 9: true, 10: false},
			count:  9,
			segs:   1,
		},
		{
			name:   "分离段",
			insert: [][2]int64{{10, 12}, {1, 3}, {20, 25}},
			want:   map[int64]bool{2: true, 3: false, 11: true, 12: false, 24: true},
			count:  9,
			segs:   3,
		},
		{
			name:   "大跨越桥接合并",
			insert: [][2]int64{{1, 4}, {10, 13}, {3, 11}},
			want:   map[int64]bool{1: true, 12: true, 13: false},
			count:  12,
			segs:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for _, iv := range tc.insert {
				s.Insert(iv[0], iv[1])
			}
			for x, w := range tc.want {
				if got := s.Contains(x); got != w {
					t.Fatalf("Contains(%d)=%v want %v", x, got, w)
				}
			}
			if s.Count() != tc.count {
				t.Fatalf("Count=%d want %d", s.Count(), tc.count)
			}
			if s.Segments() != tc.segs {
				t.Fatalf("Segments=%d want %d", s.Segments(), tc.segs)
			}
		})
	}
}

func TestFirstBefore(t *testing.T) {
	s := New()
	s.Insert(10, 20)
	s.Insert(30, 40)
	if v, ok := s.FirstBefore(35); !ok || v != 34 {
		t.Fatalf("FirstBefore(35)=(%d,%v) want 34,true", v, ok)
	}
	if v, ok := s.FirstBefore(10); ok {
		t.Fatalf("FirstBefore(10)=(%d,%v) want none", v, ok)
	}
	if v, ok := s.FirstBefore(30); !ok || v != 19 {
		t.Fatalf("FirstBefore(30)=(%d,%v) want 19,true", v, ok)
	}
}

// TestRandomModel 用朴素布尔数组对照插入与查询，保证区间合并不出错。
func TestRandomModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 2000
	const span = 2030
	var model [span]bool
	s := New()
	for it := 0; it < 3000; it++ {
		l := int64(rng.Intn(n))
		r := l + int64(rng.Intn(20))
		s.Insert(l, r)
		for x := l; x < r; x++ {
			model[x] = true
		}
	}
	var cnt int64
	for x, v := range model {
		if s.Contains(int64(x)) != v {
			t.Fatalf("seq %d mismatch", x)
		}
		if v {
			cnt++
		}
	}
	if s.Count() != cnt {
		t.Fatalf("count %d want %d", s.Count(), cnt)
	}
}

// TestVisitedBound 构造 g 个均匀分离段，验证一次查找经过节点数受对数界约束；
// g=10 与 g=10000 两档对照，证明不随 g 线性增长。
func TestVisitedBound(t *testing.T) {
	for _, g := range []int{10, 10000} {
		g := g
		t.Run("", func(t *testing.T) {
			s := New()
			const gap int64 = 10
			for i := 0; i < g; i++ {
				base := int64(i) * gap
				s.Insert(base, base+1)
			}
			if s.Segments() != g {
				t.Fatalf("g=%d segs=%d", g, s.Segments())
			}
			worst := 0
			for i := 0; i < g; i++ {
				base := int64(i) * gap
				s.Contains(base) // 命中
				if s.Visited() > worst {
					worst = s.Visited()
				}
				s.Contains(base + gap - 1) // 落在缺口内
				if s.Visited() > worst {
					worst = s.Visited()
				}
			}
			b := bound(g)
			t.Logf("g=%d worst visited=%d bound=%d", g, worst, b)
			if worst > b {
				t.Fatalf("g=%d visited %d 超过界 %d", g, worst, b)
			}
			if g == 10000 && worst >= g {
				t.Fatalf("visited 随 g 线性增长: %d", worst)
			}
		})
	}
}

// TestInOrderInsertVisited 全部按序到达（只含一个段）时反复检查新序号的查找代价。
func TestInOrderInsertVisited(t *testing.T) {
	for _, g := range []int{10, 10000} {
		s := New()
		worst := 0
		// g 个分离段模拟“缺口环境”，按序命中段首。
		for i := 0; i < g; i++ {
			base := int64(i)*10 + 5
			s.Insert(base, base+1)
		}
		for i := 0; i < g; i++ {
			s.Contains(int64(i)*10 + 5)
			if s.Visited() > worst {
				worst = s.Visited()
			}
		}
		b := bound(g)
		t.Logf("inorder g=%d worst=%d bound=%d", g, worst, b)
		if worst > b {
			t.Fatalf("g=%d visited %d > %d", g, worst, b)
		}
	}
}
