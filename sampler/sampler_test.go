package sampler

import (
	"fmt"
	"reflect"
	"testing"
)

func scriptedRNG(js map[int]int) func(int) int {
	return func(i int) int {
		if j, ok := js[i]; ok {
			return j
		}
		return 1 // 缺省落在 [1,i]，在线与朴素重放共用同一纯函数随机源
	}
}

func mkElems(n int) []string {
	es := make([]string, n)
	for i := range es {
		es[i] = fmt.Sprintf("e%d", i)
	}
	return es
}

// 不变量 1：处理 N 个元素后样本恰为 min(k,N)，槽 1..min(k,N) 全满。
func TestSizeAndSlots(t *testing.T) {
	for _, c := range []struct{ k, n int }{{3, 0}, {3, 1}, {3, 3}, {3, 6}, {5, 100}, {1, 50}, {10, 4}} {
		t.Run(fmt.Sprintf("k=%d,n=%d", c.k, c.n), func(t *testing.T) {
			s, err := New(c.k, scriptedRNG(nil))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.FeedMany(mkElems(c.n)); err != nil {
				t.Fatal(err)
			}
			got := s.Sample() // 槽位全满由 len==min(k,N) 且输入均非空保证
			if len(got) != min(c.k, c.n) || s.Size() != c.n {
				t.Fatalf("len=%d want %d, Size=%d want %d", len(got), min(c.k, c.n), s.Size(), c.n)
			}
		})
	}
}

// 不变量 2：N<=k 时全部元素按到达顺序保留。
func TestFirstKRetainedInOrder(t *testing.T) {
	for _, k := range []int{1, 3, 8} {
		s, _ := New(k, scriptedRNG(nil))
		es := make([]string, k)
		for i := range es {
			es[i] = fmt.Sprintf("v%d", i)
		}
		if err := s.FeedMany(es); err != nil {
			t.Fatal(err)
		}
		if got := s.Sample(); !reflect.DeepEqual(got, es) {
			t.Fatalf("k=%d got %v want %v", k, got, es)
		}
	}
}

// 不变量 3：在线分批处理与先收齐再重放，逐槽一致（含循环生成的大序列）。
func TestMatchesNaiveReplay(t *testing.T) {
	js := map[int]int{4: 2, 5: 4, 6: 1} // 第三节的注入序列
	cases := []struct {
		k   int
		es  []string
		rng func(int) int
	}{
		{3, []string{"A", "B", "C", "D", "E", "F"}, scriptedRNG(js)},
		{2, []string{"a", "b", "c", "d"}, scriptedRNG(js)},
		{1, []string{"x", "y", "z", "w"}, scriptedRNG(js)},
		{7, mkElems(500), func(i int) int { return (3*i+2)%i + 1 }},
	}
	for ci, c := range cases {
		t.Run(fmt.Sprintf("case%d", ci), func(t *testing.T) {
			s, _ := New(c.k, c.rng)
			if err := s.FeedMany(c.es[:2]); err != nil || s.FeedMany(c.es[2:]) != nil {
				t.Fatal(err) // 分批喂，覆盖批量路径
			}
			want, err := NaiveReplay(c.k, c.rng, c.es)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Sample(); !reflect.DeepEqual(got, want) {
				t.Fatalf("online %v != naive %v", got, want)
			}
		})
	}
}

// 不变量 4：任一条被拒整批不生效、之后仍可用；构造期两类错误可判定。
func TestRejectedBatchLeavesStateUntouched(t *testing.T) {
	for _, c := range []struct {
		name   string
		bad    []string
		inject bool
		badJ   int
	}{
		{"empty at end", []string{"G", ""}, false, 0},
		{"j below 1", []string{"G"}, true, 0},
		{"j above i", []string{"G"}, true, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			js, calls7 := map[int]int{4: 2, 5: 4, 6: 1}, 0
			rng := func(i int) int { // 第7步首次注入坏值，重试恢复为 1
				if i == 7 && c.inject {
					calls7++
					if calls7 == 1 {
						return c.badJ
					}
				}
				return scriptedRNG(js)(i)
			}
			s, _ := New(3, rng)
			if err := s.FeedMany([]string{"A", "B", "C", "D", "E", "F"}); err != nil {
				t.Fatal(err)
			}
			saved := s.Sample()
			if err := s.FeedMany(c.bad); err == nil {
				t.Fatal("expected rejection, got nil")
			}
			if got := s.Sample(); !reflect.DeepEqual(got, saved) || s.Size() != 6 {
				t.Fatalf("state changed after reject: %v size=%d", got, s.Size())
			}
			if err := s.FeedMany([]string{"G"}); err != nil {
				t.Fatalf("unusable after reject: %v", err)
			}
		})
	}
	// k<=0 与 nil rng 两类构造期错误，由 api 包 TestFourErrorsDistinct 钉住同一 New 路径。
}

// 复杂度：大 m 下一次 Sample 访问的已存元素个数恒等于 k（不随 m 增长）。
func TestSampleVisitsEqualsK(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s, _ := New(5, func(i int) int { return (i*3+1)%i + 1 })
		if err := s.FeedMany(mkElems(m)); err != nil {
			t.Fatal(err)
		}
		got := s.Sample()
		if int(s.sampleVisits.Load()) != 5 || len(got) != 5 {
			t.Fatalf("m=%d visits=%d len=%d want k=5", m, s.sampleVisits.Load(), len(got))
		}
	}
}
