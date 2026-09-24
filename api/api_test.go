package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func sampleEdges() [][2]string {
	return [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "d"},
		{"b", "d"}, {"d", "e"}, {"e", "b"},
	}
}

func tuplesEqual(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func refTuples(edges [][2]string) [][2]string {
	s := referenceClosure(edges).Sorted()
	out := make([][2]string, len(s))
	for i, t := range s {
		out[i] = [2]string{t.X, t.Y}
	}
	return out
}

// TestClosureMatchesBFS 钉住不变量 1：多种图上与朴素 BFS 可达性逐元组相同；
// 随机打乱边的到达顺序，结果确定且一致。
func TestClosureMatchesBFS(t *testing.T) {
	cases := [][][2]string{
		sampleEdges(),
		{{"a", "b"}},
		{{"a", "b"}, {"b", "a"}},
		{{"1", "2"}, {"2", "3"}, {"3", "4"}, {"1", "4"}},
		{{"x", "y"}, {"y", "z"}, {"p", "q"}}, // 两个互不连通分量
		{},
	}
	for ci, edges := range cases {
		v, err := New(edges)
		if err != nil {
			t.Fatalf("case %d: unexpected error %v", ci, err)
		}
		if got := v.Eval(); !tuplesEqual(got, refTuples(edges)) {
			t.Fatalf("case %d: closure %v != BFS %v", ci, got, refTuples(edges))
		}
		if v.Size() != len(v.Eval()) {
			t.Fatalf("case %d: Size mismatch", ci)
		}
	}
	base := sampleEdges()
	want := refTuples(base)
	for seed := int64(0); seed < 8; seed++ {
		shuffled := append([][2]string(nil), base...)
		rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		v, _ := New(shuffled)
		if got := v.Eval(); !tuplesEqual(got, want) {
			t.Fatalf("seed %d: order-dependent result %v", seed, got)
		}
	}
}

// TestRejectedLeavesNoTrace 钉住不变量 4：三类哨兵互不相同、整体拒绝、状态不变且可继续使用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		want  error
	}{
		{"empty-x", [][2]string{{"", "b"}, {"a", "b"}}, ErrEmptyNode},
		{"empty-y", [][2]string{{"a", ""}}, ErrEmptyNode},
		{"duplicate", [][2]string{{"a", "b"}, {"c", "d"}, {"a", "b"}}, ErrDuplicateEdge},
		{"self-loop", [][2]string{{"a", "b"}, {"b", "b"}}, ErrSelfLoop},
	}
	good, _ := New(sampleEdges())
	before := good.Eval()
	for _, c := range cases {
		if _, err := New(c.edges); !errors.Is(err, c.want) {
			t.Fatalf("%s: want %v got %v", c.name, c.want, err)
		}
	}
	if ErrEmptyNode == ErrSelfLoop || ErrSelfLoop == ErrDuplicateEdge || ErrEmptyNode == ErrDuplicateEdge {
		t.Fatal("the three sentinel errors are not distinct")
	}
	if !tuplesEqual(good.Eval(), before) || good.Size() != 20 {
		t.Fatal("rejected construction changed existing evaluator state")
	}
	again, err := New(sampleEdges())
	if err != nil || again.Size() != 20 {
		t.Fatal("evaluator unusable after rejections")
	}
}

// TestConcurrentEval 钉住并发：N 个 goroutine 独立求值结果逐元组相同，并并发读
// 共享求值器结果集与 SelfCheck（-race 干净）；不使用 sleep。
func TestConcurrentEval(t *testing.T) {
	const n = 32
	var wg sync.WaitGroup
	results := make([][][2]string, n)
	shared, _ := New(sampleEdges())
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := New(sampleEdges())
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = v.Eval()
			if err := shared.SelfCheck(); err != nil {
				t.Error(err)
			}
			_ = shared.Size()
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !tuplesEqual(results[i], results[0]) || len(results[i]) != 20 {
			t.Fatalf("goroutine %d got divergent result", i)
		}
	}
}

// TestSelfCheck 对若干内置边集核验四条不变量全部成立。
func TestSelfCheck(t *testing.T) {
	for _, edges := range [][][2]string{sampleEdges(), {{"a", "b"}, {"b", "a"}}, {{"a", "b"}}} {
		v, err := New(edges)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck failed: %v", err)
		}
	}
}
