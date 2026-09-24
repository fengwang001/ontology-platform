package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gset"
)

var eightFacts = []api.Fact{
	{A: "x", B: "p", C: "u", M: 10}, {A: "x", B: "p", C: "v", M: 20},
	{A: "x", B: "q", C: "u", M: 5}, {A: "y", B: "p", C: "u", M: 7},
	{A: "x", B: "p", C: "v", M: -20}, {A: "y", B: "p", C: "u", M: 3},
	{A: "x", B: "p", C: "u", M: -10}, {A: "x", B: "q", C: "u", M: 2},
}

func mustG(d []int) gset.Group { g, _ := gset.Parse(d); return g }

// 八步推导（第三节）：逐步钉住组 () 的 sum；终态整体 DeepEqual 三个组，
// want 中无 (x,p) 即钉住第 7 步归零移除与第 8 步 live 条目数。
func TestEightStepDerivation(t *testing.T) {
	v, _ := api.New([][]int{{0}, {0, 1}, {}})
	wantTotal := []int64{10, 30, 35, 42, 22, 25, 15, 17}
	for i, f := range eightFacts {
		if err := v.Apply(f); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if got := v.View()[7][""]; got != wantTotal[i] {
			t.Fatalf("step %d total=%d want %d", i+1, got, wantTotal[i])
		}
	}
	gA, gAB := mustG([]int{0}), mustG([]int{0, 1})
	want := map[int]map[string]int64{
		7: {"": 17},
		3: {gA.Key([3]string{"x", "", ""}): 7, gA.Key([3]string{"y", "", ""}): 10},
		1: {gAB.Key([3]string{"x", "q", ""}): 7, gAB.Key([3]string{"y", "p", ""}): 10},
	}
	if !reflect.DeepEqual(v.View(), want) {
		t.Fatalf("final view=%v want %v (zeroed (x,p) must be absent)", v.View(), want)
	}
}

// I2 + 构造校验：合法分组集的组 ID 集合恰好匹配（不多不少）；空/重复/越界被拒。
func TestGroupCompleteness(t *testing.T) {
	for _, c := range []struct {
		g    [][]int
		want map[int]bool
	}{
		{[][]int{{1}, {2}}, map[int]bool{5: true, 6: true}},
		{[][]int{{0, 1, 2}}, map[int]bool{0: true}},
		{[][]int{nil}, map[int]bool{7: true}},
	} {
		v, err := api.New(c.g)
		if err != nil {
			t.Fatalf("New(%v): %v", c.g, err)
		}
		v.Apply(api.Fact{A: "a", B: "b", C: "c", M: 1})
		got := map[int]bool{}
		for id := range v.View() {
			got[id] = true
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ids=%v want %v", got, c.want)
		}
	}
	for _, g := range [][][]int{{}, {{0}, {0}}, {{0, 1}, {1, 0}}, {nil, nil}, {{3}}, {{-1}}, {{0, 0}}} {
		if _, err := api.New(g); !errors.Is(err, api.ErrInvalidGroupingSets) {
			t.Errorf("New(%v) err=%v want ErrInvalidGroupingSets", g, err)
		}
	}
	v, _ := api.New([][]int{{0}}) // View 深拷贝：外部改写不污染内部。
	v.Apply(api.Fact{A: "a", B: "b", C: "c", M: 3})
	snap := v.View()
	snap[3]["1:a"] = 999
	if v.View()[3]["1:a"] != 3 {
		t.Fatal("returned View is not a defensive copy")
	}
}

// 四类哨兵互不相同；坏事实精确判定、拒绝不留痕、拒绝后仍可正常使用（I4）。
func TestRejectionAtomic(t *testing.T) {
	s := []error{api.ErrInvalidGroupingSets, api.ErrEmptyDimension, api.ErrZeroMeasure, api.ErrNegativeResult}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] == s[j] {
				t.Fatalf("sentinels %d==%d", i, j)
			}
		}
	}
	v, _ := api.New([][]int{{0}, {0, 1}, {}})
	v.Apply(api.Fact{A: "x", B: "p", C: "u", M: 10})
	for _, c := range []struct {
		f    api.Fact
		want error
	}{
		{api.Fact{A: "", B: "p", C: "u", M: 1}, api.ErrEmptyDimension},
		{api.Fact{A: "x", B: "", C: "u", M: 1}, api.ErrEmptyDimension},
		{api.Fact{A: "x", B: "p", C: "u", M: 0}, api.ErrZeroMeasure},
		{api.Fact{A: "x", B: "p", C: "u", M: -11}, api.ErrNegativeResult},
	} {
		before := v.View()
		if err := v.Apply(c.f); !errors.Is(err, c.want) {
			t.Errorf("Apply(%+v) err=%v want %v", c.f, err, c.want)
		}
		if !reflect.DeepEqual(v.View(), before) {
			t.Fatalf("rejected Apply(%+v) mutated state", c.f)
		}
	}
	if err := v.Apply(api.Fact{A: "x", B: "p", C: "u", M: 2}); err != nil || v.View()[3]["1:x"] != 12 {
		t.Fatalf("view broken after rejections: %v", v.View())
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 并发只读：N 个 goroutine 并发 View/SelfCheck，视图逐 (组,键) 一致；-race 钉住。
func TestConcurrentReadOnly(t *testing.T) {
	v, _ := api.New([][]int{{0}, {0, 1}, {}})
	for i := 0; i < 50; i++ {
		v.Apply(api.Fact{A: "a", B: "b", C: "c", M: 1})
	}
	const n = 32
	views := make([]map[int]map[string]int64, n)
	selfErr := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 1 {
				selfErr[i] = v.SelfCheck()
			}
			views[i] = v.View()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if selfErr[i] != nil || !reflect.DeepEqual(views[0], views[i]) {
			t.Fatalf("i=%d SelfCheck=%v or divergent view", i, selfErr[i])
		}
	}
}
