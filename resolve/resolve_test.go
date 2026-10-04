package resolve

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

type wantEmit struct {
	payload string
	wall    int64
	source  Source
}

type op struct {
	kind    string
	dev     int64
	boot    int64
	k       int64
	w       int64
	payload string
	err     error
}

func runCase(t *testing.T, pmax int, ops []op) [][]wantEmit {
	t.Helper()
	r, err := New(pmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	devices := map[int64]bool{}
	var all [][]wantEmit
	for i, in := range ops {
		if in.kind == "register" {
			if !devices[in.dev] {
				r.Register(in.dev)
				devices[in.dev] = true
			}
			continue
		}
		var got []Emit
		var callErr error
		if in.kind == "record" {
			got, callErr = r.Record(in.dev, in.boot, in.k, in.payload)
		} else {
			got, callErr = r.Sync(in.dev, in.boot, in.k, in.w)
		}
		if !errors.Is(callErr, in.err) {
			t.Fatalf("op %d %s: err=%v want=%v", i, in.kind, callErr, in.err)
		}
		var row []wantEmit
		for _, e := range got {
			row = append(row, wantEmit{e.Record.Payload.(string), e.Wall, e.Source})
		}
		all = append(all, row)
		if in.err != nil && len(row) != 0 {
			t.Fatalf("rejected op emitted")
		}
	}
	return all
}

func flatten(rows [][]wantEmit) []wantEmit {
	var out []wantEmit
	for _, row := range rows {
		out = append(out, row...)
	}
	return out
}

func TestSpecExample(t *testing.T) {
	ops := []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"record", 1, 1, 100, 0, "r1", nil},
		{"sync", 1, 1, 200, 5000, "", nil},
		{"record", 1, 1, 300, 0, "r2", nil},
		{"sync", 1, 1, 600, 5410, "", nil},
		{"record", 1, 1, 700, 0, "r3", nil},
		{"record", 1, 2, 50, 0, "q1", nil},
		{"record", 1, 2, 80, 0, "q2", nil},
		{"record", 1, 3, 10, 0, "z1", nil},
		{"sync", 1, 3, 40, 9000, "", nil},
		{"sync", 1, 3, 20, 8990, "", nil},
		{"sync", 1, 3, 30, 8985, "", ErrSkew},
		{"record", 1, 2, 90, 0, "q3", ErrSealed},
	}
	got, want := flatten(runCase(t, 100, ops)), []wantEmit{
		{"r1", 4900, Back}, {"r2", 5102, Interp}, {"r3", 5510, Fwd},
		{"z1", 8970, Back}, {"q1", 8929, Est}, {"q2", 8959, Est},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}

func TestImmediateResolve(t *testing.T) {
	rows := runCase(t, 10, []op{
		{"register", 7, 0, 0, 0, "", nil},
		{"sync", 7, 1, 200, 5000, "", nil},
		{"sync", 7, 1, 600, 5410, "", nil},
		{"record", 7, 1, 250, 0, "x", nil},
		{"record", 7, 1, 600, 0, "exact", nil},
		{"record", 7, 1, 10, 0, "back", nil},
		{"record", 7, 1, 700, 0, "pending", nil},
	})
	got, want := flatten(rows), []wantEmit{{"x", 5051, Interp}, {"exact", 5410, Interp}, {"back", 4810, Back}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}

func TestLargeInterpolation(t *testing.T) {
	rows := runCase(t, 10, []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"sync", 1, 1, 0, 0, "", nil},
		{"sync", 1, 1, 1e9, 1e15, "", nil},
		{"record", 1, 1, 999999999, 0, "big", nil},
	})
	num := new(big.Int).Mul(big.NewInt(999999999), big.NewInt(1e15))
	num.Div(num, big.NewInt(1e9))
	got := flatten(rows)
	if len(got) != 1 || got[0].wall != num.Int64() || got[0].source != Interp {
		t.Fatalf("%#v", got)
	}
}

func TestErrorsPmaxSkippedBoots(t *testing.T) {
	runCase(t, 10, []op{
		{"sync", 9, 1, 0, 0, "", ErrNoDevice},
		{"record", 9, 0, 0, 0, "bad", ErrInvalid},
		{"register", 9, 0, 0, 0, "", nil},
		{"record", 9, 2, 1, 0, "open2", nil},
		{"record", 9, 1, 1, 0, "sealed", ErrSealed},
		{"sync", 9, 1, 1, 1, "", ErrSealed},
		{"sync", 9, 2, 10, 100, "", nil},
		{"sync", 9, 2, 10, 101, "", ErrDupSync},
		{"sync", 9, 2, 11, 100, "", ErrSkew},
	})
	rows := runCase(t, 2, []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"record", 1, 1, 1, 0, "a", nil},
		{"record", 1, 1, 2, 0, "b", nil},
		{"record", 1, 1, 3, 0, "c", ErrFull},
		{"sync", 1, 1, 1, 11, "", nil},
		{"sync", 1, 1, 2, 12, "", nil},
		{"record", 1, 1, 0, 0, "d", nil},
	})
	if got, want := fmt.Sprint(flatten(rows)), "[{a 11 0} {b 12 0} {d 10 1}]"; got != want {
		t.Fatalf("%s", got)
	}
	chain := flatten(runCase(t, 100, []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"record", 1, 1, 5, 0, "old", nil},
		{"sync", 1, 1, 5, 100, "", nil},
		{"record", 1, 2, 10, 0, "b2", nil},
		{"record", 1, 3, 20, 0, "b3", nil},
		{"record", 1, 4, 30, 0, "b4", nil},
		{"record", 1, 5, 40, 0, "b5", nil},
		{"sync", 1, 5, 40, 1000, "", nil},
	}))
	wantChain := "[{old 100 0} {b5 1000 0} {b4 959 3} {b3 928 3} {b2 907 3}]"
	if got := fmt.Sprint(chain); got != wantChain {
		t.Fatalf("%s", got)
	}
}

func TestRejectedFutureSyncDoesNotSeal(t *testing.T) {
	rows := runCase(t, 1, []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"record", 1, 1, 1, 0, "held", nil},
		{"sync", 1, 2, 10, 100, "", ErrFull},
		{"sync", 1, 1, 10, 100, "", nil},
		{"record", 1, 2, 5, 0, "newBoot", nil},
	})
	got := flatten(rows)
	want := []wantEmit{{"held", 91, Back}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}
