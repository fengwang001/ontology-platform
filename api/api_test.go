package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// 四类故障注入：各自命中互不相同的哨兵；拒绝后引擎状态保持不变，仍可继续使用。
func TestSentinelErrorsDistinct(t *testing.T) {
	e, err := api.New([]string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetProjection([]api.Output{{Out: "x", Refs: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	for _, cols := range [][]string{{}, {"a", ""}, {"a", "a"}} {
		if _, err := api.New(cols); !errors.Is(err, api.ErrInvalidSchema) {
			t.Errorf("New(%v) err=%v, want ErrInvalidSchema", cols, err)
		}
	}
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"unknown ref", func() error {
			return e.SetProjection([]api.Output{{Out: "z", Refs: []string{"nope"}}})
		}, api.ErrUnknownRef},
		{"duplicate output", func() error {
			return e.SetProjection([]api.Output{
				{Out: "z", Refs: []string{"a"}}, {Out: "z", Refs: []string{"b"}},
			})
		}, api.ErrDuplicateOutput},
		{"row missing column", func() error {
			return e.Apply(map[string]int{"a": 1, "b": 2})
		}, api.ErrInvalidRow},
		{"row unknown column", func() error {
			return e.Apply(map[string]int{"a": 1, "b": 2, "c": 3, "x": 4})
		}, api.ErrInvalidRow},
	}
	all := []error{api.ErrInvalidSchema, api.ErrUnknownRef, api.ErrDuplicateOutput, api.ErrInvalidRow}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Fatalf("sentinels %v and %v are not distinct", all[i], all[j])
			}
		}
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
	}
}

// 不变量4：任何被拒绝的操作都不得改变投影与已产出视图。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e, _ := api.New([]string{"a", "b", "c"})
	if err := e.SetProjection([]api.Output{{Out: "x", Refs: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	good := map[string]int{"a": 1, "b": 2, "c": 3}
	if err := e.Apply(good); err != nil {
		t.Fatal(err)
	}
	names, view := e.OutputNames(), e.View()
	badProjs := [][]api.Output{
		{{Out: "z", Refs: []string{"nope"}}},
		{{Out: "z", Refs: []string{"a"}}, {Out: "z", Refs: []string{"b"}}},
	}
	for _, bp := range badProjs {
		if err := e.SetProjection(bp); err == nil {
			t.Fatal("bad projection unexpectedly accepted")
		}
	}
	if err := e.Apply(map[string]int{"a": 1}); err == nil {
		t.Fatal("short row unexpectedly accepted")
	}
	if err := e.Apply(map[string]int{"a": 1, "b": 2, "c": 3, "z": 0}); err == nil {
		t.Fatal("row with unknown column unexpectedly accepted")
	}
	if gotN, gotV := e.OutputNames(), e.View(); !reflect.DeepEqual(gotN, names) || !reflect.DeepEqual(gotV, view) {
		t.Fatalf("state changed after rejections: names=%v view=%v", gotN, gotV)
	}
	// 被拒后仍可正常使用。
	if err := e.Apply(good); err != nil {
		t.Fatalf("engine unusable after rejection: %v", err)
	}
	if got := e.View(); len(got) != 2 || !reflect.DeepEqual(got[1], []int{1}) {
		t.Fatalf("post-rejection apply state wrong: %v", got)
	}
}

// 不变量（并发）：喂好若干行后，多个 goroutine 并发只读 View/OutputNames，
// 各自必须拿到逐行逐列相同的结果；全程不使用 sleep。
func TestConcurrentViewReaders(t *testing.T) {
	e, _ := api.New([]string{"a", "b", "c", "d", "e"})
	if err := e.SetProjection([]api.Output{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 50; n++ {
		if err := e.Apply(map[string]int{"a": n, "b": n + 1, "c": n + 2, "d": n + 3, "e": -n}); err != nil {
			t.Fatal(err)
		}
	}
	want := e.View()
	const readers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := false
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := e.View()
			names := e.OutputNames()
			ok := reflect.DeepEqual(got, want) && reflect.DeepEqual(names, []string{"x", "sum", "y"})
			if !ok {
				mu.Lock()
				bad = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if bad {
		t.Fatal("concurrent readers observed inconsistent view or names")
	}
}

// SelfCheck 是可被测试直接调用的内置自检：返回 nil 即四条不变量全部成立。
func TestSelfCheck(t *testing.T) {
	e, err := api.New([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
