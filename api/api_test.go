package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// TestEightSteps replays the mandated eight operations row by row.
func TestEightSteps(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	type row struct {
		name string
		got  int
		err  error
		want int
	}
	var rows []row
	add := func(x, y int, want int) {
		i, err := api.Add(x, y)
		rows = append(rows, row{"add", i, err, want})
	}
	add(0, 0, 0)
	add(1, 1, 1)
	add(4, 0, 2)
	n4, e4 := api.Nearest(0, 2)
	rows = append(rows, row{"nearest", n4, e4, 1})
	n5, e5 := api.Nearest(1, 0)
	rows = append(rows, row{"nearest tie", n5, e5, 0})
	add(0, 2, 3)
	n7, e7 := api.Nearest(0, 2)
	rows = append(rows, row{"nearest", n7, e7, 3})
	n8, e8 := api.Within(1, 0, 1)
	rows = append(rows, row{"within", n8, e8, 0})
	for i, r := range rows {
		if r.err != nil || r.got != r.want {
			t.Fatalf("step %d %s: got (%d,%v) want %d", i+1, r.name, r.got, r.err, r.want)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{0, 0}, {1, 1}, {4, 0}} {
		if _, err := api.Add(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name     string
		call     func() (int, error)
		want     error
		distinct []error
	}{
		{"out of bounds high", func() (int, error) { return api.Add(10001, 0) }, api.ErrOutOfBounds,
			[]error{api.ErrDuplicate, api.ErrNegativeRadius}},
		{"out of bounds low", func() (int, error) { return api.Add(0, -10001) }, api.ErrOutOfBounds, nil},
		{"duplicate", func() (int, error) { return api.Add(1, 1) }, api.ErrDuplicate,
			[]error{api.ErrOutOfBounds, api.ErrNegativeRadius}},
		{"negative radius", func() (int, error) { return api.Within(0, 0, -1) }, api.ErrNegativeRadius,
			[]error{api.ErrOutOfBounds, api.ErrDuplicate}},
	}
	for _, c := range cases {
		if _, err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		for _, other := range c.distinct {
			if errors.Is(errOrPanic(c.call), other) {
				t.Fatalf("%s error must be distinct from %v", c.name, other)
			}
		}
	}
	// Rejected operations leave no trace: next valid Add must be index 3 and
	// the registry still answers correctly.
	if idx, err := api.Add(7, -7); err != nil || idx != 3 {
		t.Fatalf("after rejects Add=(%d,%v), want index 3", idx, err)
	}
	if i, err := api.Nearest(1, 0); err != nil || i != 0 {
		t.Fatalf("after rejects Nearest=(%d,%v), want 0 (tie -> smallest index)", i, err)
	}
}

func errOrPanic(f func() (int, error)) (err error) {
	defer func() { _ = recover() }()
	_, err = f()
	return err
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := api.Add(i*7%9000-4500, i*13%9000-4500); err != nil {
			t.Fatal(err)
		}
	}
	const nG = 24
	var wg sync.WaitGroup
	results := make([][4]int, nG)
	errs := make(chan error, nG)
	for g := 0; g < nG; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var out [4]int
			for i, q := range [4][2]int{{123, -456}, {0, 0}, {-8000, 8000}, {4500, 4500}} {
				idx, err := api.Nearest(q[0], q[1])
				if err != nil {
					errs <- err
					return
				}
				out[i] = idx
			}
			results[g] = out
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for g := 1; g < nG; g++ {
		if results[g] != results[0] {
			t.Fatalf("goroutine %d got %v want %v", g, results[g], results[0])
		}
	}
}
