package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// TestSelfCheck exercises the exported self-check and, in table-driven
// form, the externally observable behaviour behind each invariant.
func TestSelfCheck(t *testing.T) {
	tb := api.New()
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}

	t.Run("eight steps", func(t *testing.T) {
		tb := api.New()
		steps := []struct {
			run  func() error
			want error // nil means success
		}{
			{func() error { return tb.Declare("x", api.Int) }, nil},
			{func() error { tb.Enter(); return nil }, nil},
			{func() error { return tb.Declare("y", api.Bool) }, nil},
			{func() error { return tb.Declare("x", api.Bool) }, nil},
			{func() error { v, e := tb.Lookup("x"); return expectVal(e, v, api.Bool) }, nil},
			{func() error { return tb.Exit() }, nil},
			{func() error { v, e := tb.Lookup("x"); return expectVal(e, v, api.Int) }, nil},
			{func() error { _, e := tb.Lookup("y"); return e }, api.ErrUndeclared},
		}
		for i, st := range steps {
			if e := st.run(); !errors.Is(e, st.want) {
				t.Fatalf("step %d: %v want %v", i+1, e, st.want)
			}
		}
	})

	t.Run("distinct sentinels", func(t *testing.T) {
		errs := []error{api.ErrDuplicate, api.ErrUndeclared, api.ErrExitGlobal}
		for i := range errs {
			for j := i + 1; j < len(errs); j++ {
				if errs[i] == errs[j] {
					t.Fatalf("sentinels %d and %d are identical", i, j)
				}
			}
		}
	})
}

// TestRejectedOpsExternal verifies rejected calls leave no trace via
// the public API only.
func TestRejectedOpsExternal(t *testing.T) {
	cases := []struct {
		name string
		do   func(*api.Table) error
	}{
		{"duplicate", func(t *api.Table) error { return t.Declare("x", api.Bool) }},
		{"undeclared", func(t *api.Table) error { _, e := t.Lookup("nope"); return e }},
		{"exit-global", func(t *api.Table) error { return t.Exit() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := api.New()
			if e := tb.Declare("x", api.Int); e != nil {
				t.Fatal(e)
			}
			before := tb.RenderStack()
			if e := tc.do(tb); e == nil {
				t.Fatal("want an error")
			}
			if after := tb.RenderStack(); after != before {
				t.Fatalf("state changed: %s -> %s", before, after)
			}
			if v, e := tb.Lookup("x"); e != nil || v != api.Int {
				t.Fatalf("x after rejection: %v %v", v, e)
			}
		})
	}
}

// TestConcurrentLookup pins section 6 through the public API: N
// goroutines look up shadowed and outer names and must agree exactly.
func TestConcurrentLookup(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		tb := api.New()
		if e := tb.Declare("w", api.Int); e != nil {
			t.Fatal(e)
		}
		if e := tb.Declare("x", api.Int); e != nil {
			t.Fatal(e)
		}
		tb.Enter()
		if e := tb.Declare("x", api.Bool); e != nil {
			t.Fatal(e)
		}
		res := make([][2]api.T, n)
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				res[g][0], _ = tb.Lookup("x") // shadowed -> bool
				res[g][1], _ = tb.Lookup("w") // outer -> int
			}(g)
		}
		wg.Wait()
		for g := 1; g < n; g++ {
			if res[g] != res[0] {
				t.Fatalf("n=%d: goroutine %d got %v want %v", n, g, res[g], res[0])
			}
		}
		if res[0] != [2]api.T{api.Bool, api.Int} {
			t.Fatalf("n=%d: values %v", n, res[0])
		}
	}
}

func expectVal(err error, got, want api.T) error {
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("got " + got.String() + ", want " + want.String())
	}
	return nil
}
