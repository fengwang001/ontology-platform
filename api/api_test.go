package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/closure"
	"ontology/graph"
)

// TestTenSteps：第三节十步推导，逐步钉住 |R| 与删边步的过删数/再推导数。
func TestTenSteps(t *testing.T) {
	eng, _ := New(10)
	type op struct {
		add  bool
		u, v string
	}
	ops := []op{{true, "A", "B"}, {true, "B", "C"}, {true, "A", "C"}, {true, "C", "A"}, {true, "B", "C"},
		{false, "B", "C"}, {false, "A", "B"}, {true, "C", "D"}, {false, "C", "A"}, {false, "B", "C"}}
	wantSize := []int{1, 3, 3, 9, 9, 9, 6, 9, 5, 3}
	wantODRE := [][2]int{{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {0, 0}, {9, 6}, {-1, -1}, {9, 5}, {2, 0}}
	for i, o := range ops {
		od, re := -1, -1
		var err error
		if o.add {
			err = eng.AddEdge(o.u, o.v)
		} else {
			od, re, err = eng.RemoveEdge(o.u, o.v)
		}
		if w := wantODRE[i]; err != nil || len(eng.Pairs()) != wantSize[i] || od != w[0] || re != w[1] {
			t.Fatalf("步%d: err=%v |R|=%d 过删/再推=%d/%d", i+1, err, len(eng.Pairs()), od, re)
		}
		if i == 6 && !reflect.DeepEqual(eng.Pairs(),
			[][2]string{{"A", "A"}, {"A", "C"}, {"B", "A"}, {"B", "C"}, {"C", "A"}, {"C", "C"}}) {
			t.Fatalf("步7后 R=%v", eng.Pairs())
		}
	}
}

// TestMultiplicity：R 只取决于存在的边集；重数降为 0 之前的删除不改变 R。
func TestMultiplicity(t *testing.T) {
	eng, _ := New(10)
	eng.AddEdge("a", "b")
	eng.AddEdge("b", "c")
	full := eng.Pairs()
	for i := 0; i < 3; i++ { // 重数加到 4
		eng.AddEdge("a", "b")
	}
	for i := 0; i < 3; i++ { // 减到 1，边仍在，R 不变
		if od, re, err := eng.RemoveEdge("a", "b"); err != nil || od != 0 || re != 0 {
			t.Fatalf("重数仍正时不应动R: od=%d re=%d err=%v", od, re, err)
		}
		if !reflect.DeepEqual(eng.Pairs(), full) {
			t.Fatal("重数>0 时 R 应不变")
		}
	}
	if _, _, err := eng.RemoveEdge("a", "b"); err != nil || eng.Reachable("a", "b") || !eng.Reachable("b", "c") {
		t.Fatal("重数归零后 R 应只取决于存在的边")
	}
}

// TestErrors：四类故障注入各有可判定且互不相同的哨兵错误，被拒后状态不变。
func TestErrors(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrBadMaxEdges) {
		t.Fatalf("New(0): %v", err)
	}
	eng, _ := New(1)
	eng.AddEdge("a", "b")
	before := eng.Pairs()
	cases := []func() error{
		func() error { return eng.AddEdge("", "x") },
		func() error { _, _, e := eng.RemoveEdge("a", "c"); return e },
		func() error { return eng.AddEdge("c", "d") },
	}
	wants := []error{ErrEmptyNode, ErrEdgeNotFound, ErrTooManyEdges}
	for i, op := range cases {
		if err := op(); !errors.Is(err, wants[i]) || !reflect.DeepEqual(eng.Pairs(), before) {
			t.Fatalf("%v 未命中或被拒后状态改变", wants[i])
		}
	}
	all := []error{ErrBadMaxEdges, ErrEmptyNode, ErrEdgeNotFound, ErrTooManyEdges}
	for i, a := range all {
		for _, b := range all[i+1:] {
			if errors.Is(a, b) {
				t.Fatal("哨兵错误必须互不相同")
			}
		}
	}
	if _, _, err := eng.RemoveEdge("a", "b"); err != nil {
		t.Fatal("被拒后引擎应仍可用")
	}
}

// TestConcurrent：读者拿到的 Pairs 逐对相同；写者与读者并发增删后 R 等于朴素 BFS。
func TestConcurrent(t *testing.T) {
	eng, _ := New(1000)
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}} {
		eng.AddEdge(e[0], e[1])
	}
	base := eng.Pairs()
	eng2, _ := New(1000)
	gm := graph.New()
	ops := [][2]string{{"m", "n"}, {"n", "o"}, {"o", "p"}, {"m", "o"}, {"p", "m"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				eng2.Pairs()
				eng2.Reachable("m", "p")
				if !reflect.DeepEqual(eng.Pairs(), base) || eng2.SelfCheck() != nil {
					t.Error("并发读不一致或 SelfCheck 失败")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			e := ops[i%len(ops)]
			if i%2 == 0 {
				eng2.AddEdge(e[0], e[1])
				gm.Add(e[0], e[1])
			} else if gm.Has(e[0], e[1]) {
				eng2.RemoveEdge(e[0], e[1])
				gm.Remove(e[0], e[1])
			}
		}
	}()
	close(start)
	wg.Wait()
	want, got := closure.Naive(gm), eng2.Pairs()
	bad := len(got) != len(want)
	for _, p := range got {
		bad = bad || !want[p]
	}
	if bad {
		t.Fatalf("并发结束后 R 与朴素参照不一致: |R|=%d, want %d", len(got), len(want))
	}
}
