package api_test

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/agg"
	"ontology/api"
)

func eight() []api.Event {
	return []api.Event{
		{"a", 5}, {"b", 3}, {"c", 7}, {"d", 2},
		{"b", 4}, {"a", 1}, {"c", 2}, {"d", 5},
	}
}

// TestEightStepTrace 钉住第三节八行表：种类、两区内容、溢写者、计数与至多一处。
func TestEightStepTrace(t *testing.T) {
	ae := make([]agg.Event, len(eight()))
	for i, e := range eight() {
		ae[i] = agg.Event{Key: e.Key, Val: e.Val}
	}
	tr := agg.StepTrace(3, ae)
	kind := []string{"insert", "insert", "insert", "insert", "update", "reload", "reload", "reload"}
	evk := []string{"", "", "", "a", "", "c", "d", "b"}
	wantR := []map[string]int64{
		{"a": 5}, {"a": 5, "b": 3}, {"a": 5, "b": 3, "c": 7},
		{"b": 3, "c": 7, "d": 2}, {"b": 7, "c": 7, "d": 2},
		{"a": 6, "b": 7, "d": 2}, {"a": 6, "b": 7, "c": 9}, {"a": 6, "c": 9, "d": 7},
	}
	wantS := []map[string]int64{{}, {}, {}, {"a": 5}, {"a": 5}, {"c": 7}, {"d": 2}, {"b": 7}}
	for i, st := range tr {
		if st.Kind != kind[i] || st.EvictedKey != evk[i] || len(st.Resident) > 3 ||
			!reflect.DeepEqual(st.Resident, wantR[i]) || !reflect.DeepEqual(st.Spilled, wantS[i]) {
			t.Fatalf("step %d mismatch: %+v", i+1, st)
		}
		for k := range st.Resident {
			if _, dup := st.Spilled[k]; dup {
				t.Fatalf("step %d: %s in both areas", i+1, k)
			}
		}
		if i == 5 && st.Resident["a"] != 6 { // (甲) 回载必须加上溢写部分和 5
			t.Fatalf("step6 a=%d want 6", st.Resident["a"])
		}
	}
}

// TestViewMatchesBatch 表驱动 + 随机事件序：View 逐 Key 等于朴素批量累加。
func TestViewMatchesBatch(t *testing.T) {
	for _, limit := range []int{1, 2, 3, 5, 16} {
		for seed := int64(0); seed < 20; seed++ {
			rng := rand.New(rand.NewSource(seed))
			evs := make([]api.Event, 50+rng.Intn(100))
			want := map[string]int64{}
			for i := range evs {
				key := string(rune('a' + rng.Intn(26))) // 前 7 个热键频繁回载，其余多为新键
				evs[i] = api.Event{Key: key, Val: int64(rng.Intn(19) - 9)}
				want[key] += evs[i].Val
			}
			a, _ := api.New(limit) // limit 档全为正
			if bad := a.Feed(evs); bad != nil || !reflect.DeepEqual(a.View(), want) {
				t.Fatalf("limit=%d seed=%d: %v %v", limit, seed, bad, a.View())
			}
		}
	}
}

// TestSentinelErrors 三类错误非空、互不相同且可用 errors.Is 判定。
func TestSentinelErrors(t *testing.T) {
	a, _ := api.New(2)
	_, ez := api.New(0)
	_, en := api.New(-1)
	errs := []error{
		ez, en,
		a.Feed([]api.Event{{Key: "", Val: 1}}),
		a.Feed([]api.Event{{Key: "z", Val: math.MaxInt64}, {Key: "z", Val: 1}}),
		a.Feed([]api.Event{{Key: "z", Val: math.MinInt64}, {Key: "z", Val: -1}}),
	}
	wants := []error{api.ErrInvalidLimit, api.ErrInvalidLimit, api.ErrEmptyKey, api.ErrOverflow, api.ErrOverflow}
	for i := range errs {
		if !errors.Is(errs[i], wants[i]) {
			t.Fatalf("case %d: %v want %v", i, errs[i], wants[i])
		}
	}
	if ez == nil || en == nil || api.ErrInvalidLimit == api.ErrEmptyKey ||
		api.ErrEmptyKey == api.ErrOverflow || api.ErrInvalidLimit == api.ErrOverflow {
		t.Fatal("sentinel errors must be non-nil and pairwise distinct")
	}
}

// TestRejectedBatchAtomic 非法事件在批中任意位置都使整批不生效，计数不变且可继续使用。
func TestRejectedBatchAtomic(t *testing.T) {
	bads := [][]api.Event{
		{{Key: "q", Val: 1}, {Key: "", Val: 1}, {Key: "q", Val: 2}},
		{{Key: "q", Val: math.MaxInt64}, {Key: "q", Val: 1}},
		{{Key: "q", Val: 1}, {Key: "q", Val: math.MaxInt64}},
	}
	for _, bad := range bads {
		a, _ := api.New(2)
		if err := a.Feed(eight()); err != nil {
			t.Fatal(err)
		}
		snap, sp, ld := a.View(), a.Spills(), a.Loads()
		if a.Feed(bad) == nil {
			t.Fatal("batch should be rejected")
		}
		if !reflect.DeepEqual(a.View(), snap) || a.Spills() != sp || a.Loads() != ld {
			t.Fatal("rejected batch left state behind")
		}
		if err := a.Feed([]api.Event{{Key: "q", Val: 40}}); err != nil || a.View()["q"] != 40 {
			t.Fatalf("partial apply or unusable after reject: q=%d %v", a.View()["q"], err)
		}
	}
	// 跨批次：已有 MaxInt64 的 Key 再 +1 必须判溢出，且旧值保持不变。
	c, _ := api.New(1)
	_ = c.Feed([]api.Event{{Key: "p", Val: math.MaxInt64}})
	if e := c.Feed([]api.Event{{Key: "p", Val: 1}}); !errors.Is(e, api.ErrOverflow) ||
		c.View()["p"] != math.MaxInt64 {
		t.Fatalf("cross-batch overflow: err=%v p=%d", e, c.View()["p"])
	}
}

// TestConcurrentReaders 多个 goroutine 并发只读，View 逐字段相同；无 sleep。
func TestConcurrentReaders(t *testing.T) {
	a, _ := api.New(3)
	if err := a.Feed(eight()); err != nil {
		t.Fatal(err)
	}
	views := make([]map[string]int64, 32)
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			views[i] = a.View()
			_ = a.SelfCheck()
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(views); i++ {
		if !reflect.DeepEqual(views[0], views[i]) {
			t.Fatalf("reader %d differs", i)
		}
	}
}
