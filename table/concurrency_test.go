package table_test

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
	"ontology/table"
)

func TestErrorsDistinct(t *testing.T) {
	errs := []error{
		table.ErrEmptyName, table.ErrDepthLimit, table.ErrDeclLimit, table.ErrLeaveRoot,
		table.ErrSelfChain, table.ErrSelfRef,
		scope.ErrDuplicateDeclare, resolve.ErrUndefined, resolve.ErrUseBeforeDeclare,
	}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatalf("errors %d and %d are identical: %v", i, j, errs[i])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	tb := table.New(4, 8)
	_ = tb.Declare("a", name.KindStrict, 1)
	_ = tb.Enter()
	_ = tb.Declare("b", name.KindForward, 5)
	_, _ = tb.Ref("a", 2)
	_, _ = tb.Ref("b", 2)
	_ = tb.Leave()
	_ = tb.Declare("a", name.KindStrict, 3) // 被拒绝：重复声明
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestCapturesExact(t *testing.T) {
	cases := []struct {
		name  string
		setup func(tb *table.Table)
		want  []string
	}{
		{"one outer, ref twice, sibling excluded", func(tb *table.Table) {
			_ = tb.Declare("a", name.KindStrict, 1)
			_ = tb.Declare("b", name.KindStrict, 2)
			_ = tb.Enter()
			_, _ = tb.Ref("a", 3)
			_, _ = tb.Ref("a", 4)
		}, []string{"a@0"}},
		{"local decl not captured", func(tb *table.Table) {
			_ = tb.Enter()
			_ = tb.Declare("c", name.KindStrict, 1)
			_, _ = tb.Ref("c", 2)
		}, nil},
		{"local forward decl after ref pos not captured", func(tb *table.Table) {
			_ = tb.Declare("x", name.KindStrict, 1)
			_ = tb.Enter()
			_ = tb.Declare("x", name.KindForward, 5)
			_, _ = tb.Ref("x", 2)
		}, nil},
		{"two depths", func(tb *table.Table) {
			_ = tb.Declare("a", name.KindStrict, 1)
			_ = tb.Enter()
			_ = tb.Declare("b", name.KindStrict, 2)
			_ = tb.Enter()
			_, _ = tb.Ref("a", 3)
			_, _ = tb.Ref("b", 4)
		}, []string{"a@0", "b@1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := table.New(8, 8)
			tc.setup(tb)
			caps, err := tb.Captures()
			if err != nil {
				t.Fatalf("captures err=%v", err)
			}
			got := []string{}
			for _, c := range caps {
				got = append(got, fmt.Sprintf("%s@%d", c.Decl.Name, c.Depth))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestConcurrentRef(t *testing.T) {
	tb := table.New(4, 8)
	_ = tb.Declare("a", name.KindStrict, 1)
	_ = tb.Declare("b", name.KindForward, 2)
	_ = tb.Enter()
	_ = tb.Declare("c", name.KindStrict, 3)
	names := []string{"a", "b", "c"}
	want := probe(tb, names...)

	const goroutines, rounds = 32, 25
	got := make([][]resolve.Result, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < rounds; j++ {
				got[i] = probe(tb, names...)
				if _, err := tb.Captures(); err != nil {
					panic(err)
				}
				if err := tb.SelfCheck(); err != nil {
					panic(err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, r := range got {
		if len(r) != len(want) {
			t.Fatalf("goroutine %d: missing results", i)
		}
		for j := range want {
			if r[j] != want[j] {
				t.Fatalf("goroutine %d ref %d: got %+v want %+v", i, j, r[j], want[j])
			}
		}
	}
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("selfcheck after concurrency: %v", err)
	}
}
