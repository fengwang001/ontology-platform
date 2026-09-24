package walk

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/graph"
)

func demoGraph() *graph.Graph {
	// s -> b1,b2 ; b1 -> s,c ; b2 -> b1 ; c -> s （环/自环样例在其它用例）
	return graph.New(map[string][]string{
		"s":  {"b1", "b2"},
		"b1": {"s", "c"},
		"b2": {"b1"},
		"c":  {"s"},
		"z":  {"unreachable"},
	})
}

func TestDeterministicRepeats(t *testing.T) {
	g := demoGraph()
	var want []string
	// 表驱动：预算集逐一覆盖，重复二十次逐元素比较。
	budgets := []int{1, 2, 3, 4, 5, 100}
	for _, b := range budgets {
		b := b
		t.Run("budget", func(t *testing.T) {
			r, err := Walk(g, Initial("s"), b)
			if err != nil {
				t.Fatal(err)
			}
			want = r.Visited
			for i := 0; i < 20; i++ {
				got, _ := Walk(g, Initial("s"), b)
				if !reflect.DeepEqual(got.Visited, want) {
					t.Fatalf("budget %d repeat %d: %v != %v", b, i, got.Visited, want)
				}
			}
		})
	}
}

func TestSplitEquivalence(t *testing.T) {
	g := demoGraph()
	full, err := Walk(g, Initial("s"), 100)
	if err != nil {
		t.Fatal(err)
	}
	// 逐切分点 a = 1..n-1 循环覆盖。
	n := len(full.Visited)
	for a := 1; a < n; a++ {
		r1, err := Walk(g, Initial("s"), a)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := Walk(g, r1.Next, n-a)
		if err != nil {
			t.Fatal(err)
		}
		joined := append(append([]string(nil), r1.Visited...), r2.Visited...)
		if !reflect.DeepEqual(joined, full.Visited) {
			t.Fatalf("split a=%d: %v != %v", a, joined, full.Visited)
		}
		if !r2.Next.Done {
			t.Fatalf("split a=%d: second segment not done", a)
		}
	}
}

func TestBudgetEdges(t *testing.T) {
	cases := []struct {
		name   string
		budget int
	}{
		{"zero", 0}, {"one", 1}, {"all", 5}, {"overshoot", 50}}
	g := demoGraph()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, err := Walk(g, Initial("s"), tc.budget)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.budget
			if want > 5 {
				want = 5
			}
			visits, _, examined := r.Stats()
			if visits != want || len(r.Visited) != want {
				t.Fatalf("visited=%d stats=%d want=%d", len(r.Visited), visits, want)
			}
			if tc.budget == 0 && !reflect.DeepEqual(r.Next, Initial("s")) {
				t.Fatal("zero budget checkpoint must equal initial")
			}
			if tc.budget >= 5 && !r.Next.Done {
				t.Fatal("reachable set exhausted must be done")
			}
			sum := 0
			for _, id := range r.Visited {
				out, _ := g.Out(id)
				sum += len(out)
			}
			if examined > sum {
				t.Fatalf("edges examined %d > outdegree sum %d", examined, sum)
			}
		})
	}
	// 已完成续点再续传：空序列、无错误。
	full, _ := Walk(g, Initial("s"), 50)
	again, err := Walk(g, full.Next, 10)
	if err != nil || len(again.Visited) != 0 || !again.Next.Done {
		t.Fatalf("resume from done: %v %v %v", again.Visited, err, again.Next.Done)
	}
}

func TestStarPeakQueue(t *testing.T) {
	// 中心 + 一万叶子；峰值不得超过 4*预算。循环覆盖预算档位。
	g := graph.Star("center", 10000)
	budgets := []int{10, 100, 1000}
	for _, b := range budgets {
		b := b
		t.Run("budget", func(t *testing.T) {
			r, err := Walk(g, Initial("center"), b)
			if err != nil {
				t.Fatal(err)
			}
			_, peak, examined := r.Stats()
			if peak > 4*b {
				t.Fatalf("budget %d peak %d > %d", b, peak, 4*b)
			}
			if examined > b-1 { // 只有中心在展开，叶子未轮到
				t.Fatalf("budget %d examined %d", b, examined)
			}
		})
	}
}

func TestCyclesAndSelfLoop(t *testing.T) {
	g := graph.New(map[string][]string{
		"a": {"a", "b", "b"},
		"b": {"a"},
	})
	r, err := Walk(g, Initial("a"), 100)
	if err != nil || fmtSprint(r.Visited) != "[a b]" {
		t.Fatalf("self/dup/cycle: %v %v", r.Visited, err)
	}
}

func TestMissingStartAndEmpty(t *testing.T) {
	if _, err := Walk(graph.New(nil), Initial("x"), 3); !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("empty graph start: %v", err)
	}
	g := graph.New(map[string][]string{"a": {"b"}})
	if _, err := Walk(g, Initial("ghost"), 3); !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("missing start: %v", err)
	}
}

func TestMutationBetweenSegments(t *testing.T) {
	g := demoGraph()
	r1, _ := Walk(g, Initial("s"), 2) // visited s,b1；b2 仍在 Pending
	if !g.Delete("b2") {
		t.Fatal("delete b2")
	}
	_, err := Walk(g, r1.Next, 10)
	if !errors.Is(err, ErrNodeMissing) || MissingNode(err) != "b2" {
		t.Fatalf("mutation err=%v node=%q", err, MissingNode(err))
	}
}

func TestConcurrentResume(t *testing.T) {
	g := demoGraph()
	r1, _ := Walk(g, Initial("s"), 1)
	var wg sync.WaitGroup
	results := make([][]string, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := Walk(g, r1.Next, 4)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = r.Visited
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if !reflect.DeepEqual(results[i], results[0]) {
			t.Fatalf("concurrent divergence %d: %v vs %v", i, results[i], results[0])
		}
	}
}

func fmtSprint(v []string) string {
	out := "["
	for i, s := range v {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out + "]"
}
