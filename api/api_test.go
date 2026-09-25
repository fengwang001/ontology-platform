package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/col"
	"ontology/prune"
)

func newStore(t *testing.T) *api.Store {
	t.Helper()
	st, err := api.New([]string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjection([]api.Output{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

var sampleRows = []map[string]int{
	{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
	{"a": 6, "b": 7, "c": 8, "d": 9, "e": 10},
	{"a": 11, "b": 12, "c": 13, "d": 14, "e": 15},
}

// 不变量 1：投影输出与「从完整行按 refs 求和」的全量重算逐列相同。
func TestConsistentWithFullRecompute(t *testing.T) {
	st := newStore(t)
	for _, r := range sampleRows {
		if err := st.Apply(r); err != nil {
			t.Fatal(err)
		}
	}
	view := st.View()
	if len(view) != len(sampleRows) {
		t.Fatalf("view rows = %d", len(view))
	}
	for i, r := range sampleRows {
		want := []int{r["a"], r["b"] + r["c"], r["d"]} // 全量重算
		for j := range want {
			if view[i][j] != want[j] {
				t.Fatalf("row %d col %d = %d, want %d", i, j, view[i][j], want[j])
			}
		}
	}
}

// 不变量 3：输出顺序恒等于投影定义顺序，名称恒为 out。
func TestOutputOrderAndNames(t *testing.T) {
	st := newStore(t)
	got := fmt.Sprint(st.OutputNames())
	if got != "[x sum y]" {
		t.Fatalf("OutputNames = %s, want [x sum y]", got)
	}
	if err := st.Apply(sampleRows[0]); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(st.OutputNames()); got != "[x sum y]" {
		t.Fatalf("after Apply OutputNames = %s", got)
	}
}

// 不变量 4：四类被拒操作互不相同且不留痕，之后仍可正常使用。
func TestRejectedOpsLeaveStateIntact(t *testing.T) {
	st := newStore(t)
	if err := st.Apply(sampleRows[0]); err != nil {
		t.Fatal(err)
	}
	beforeView := fmt.Sprint(st.View())
	beforeNames := fmt.Sprint(st.OutputNames())

	cases := []struct {
		name string
		err  error
	}{
		{"schema empty", func() error { _, e := api.New(nil); return e }()},
		{"schema empty name", func() error { _, e := api.New([]string{"a", ""}); return e }()},
		{"schema dup", func() error { _, e := api.New([]string{"a", "a"}); return e }()},
		{"refs unknown", st.SetProjection([]api.Output{{Out: "z", Refs: []string{"nope"}}})},
		{"dup output", st.SetProjection([]api.Output{
			{Out: "x", Refs: []string{"a"}}, {Out: "x", Refs: []string{"b"}}})},
		{"row missing col", st.Apply(map[string]int{"a": 1, "b": 2})},
		{"row unknown col", st.Apply(map[string]int{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "zz": 6})},
	}
	sentinels := []error{col.ErrNoColumns, col.ErrEmptyColName, col.ErrDuplicateCol,
		prune.ErrUnknownRef, prune.ErrDuplicateOut, col.ErrMissingCol, col.ErrUnknownCol}
	for i, c := range cases {
		if c.err == nil {
			t.Fatalf("%s: accepted, want rejection", c.name)
		}
		if !errors.Is(c.err, sentinels[i]) {
			t.Fatalf("%s: err = %v, want %v", c.name, c.err, sentinels[i])
		}
		for j := range cases {
			if i != j && errors.Is(c.err, cases[j].err) {
				t.Fatalf("errors %q and %q not distinct", c.name, cases[j].name)
			}
		}
	}
	if fmt.Sprint(st.View()) != beforeView || fmt.Sprint(st.OutputNames()) != beforeNames {
		t.Fatal("rejected op changed state")
	}
	if err := st.Apply(sampleRows[1]); err != nil {
		t.Fatalf("store unusable after rejections: %v", err)
	}
}

// 并发：N 个 goroutine 只读 View，拿到的视图必须逐行逐列相同。
func TestConcurrentViewReads(t *testing.T) {
	st := newStore(t)
	for _, r := range sampleRows {
		if err := st.Apply(r); err != nil {
			t.Fatal(err)
		}
	}
	want := fmt.Sprint(st.View())
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if got := fmt.Sprint(st.View()); got != want {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("concurrent View = %s, want %s", got, want)
	}
}
