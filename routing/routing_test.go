package routing_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/routing"
)

func TestDefine(t *testing.T) {
	cases := []struct {
		name        string
		id          string
		n           int
		back        []int
		insp        []bool
		R           int
		wantErr     error
	}{
		{"ok_min", "r1", 1, []int{1}, []bool{false}, 0, nil},
		{"ok_max", "r2", 32, mkBack(32), make([]bool, 32), 1000, nil},
		{"empty_id", "", 1, []int{1}, []bool{false}, 0, routing.ErrInvalidParam},
		{"n_zero", "r3", 0, nil, nil, 0, routing.ErrInvalidParam},
		{"n_too_big", "r4", 33, mkBack(33), make([]bool, 33), 0, routing.ErrInvalidParam},
		{"back_len", "r5", 2, []int{1}, []bool{false, false}, 0, routing.ErrInvalidParam},
		{"insp_len", "r6", 2, []int{1, 2}, []bool{false}, 0, routing.ErrInvalidParam},
		{"back_zero", "r7", 2, []int{1, 0}, []bool{false, false}, 0, routing.ErrInvalidParam},
		{"back_forward", "r8", 2, []int{2, 2}, []bool{false, false}, 0, routing.ErrInvalidParam},
		{"back_self_ok", "r9", 2, []int{1, 2}, []bool{false, false}, 0, nil},
		{"R_negative", "r10", 1, []int{1}, []bool{false}, -1, routing.ErrInvalidParam},
		{"R_too_big", "r11", 1, []int{1}, []bool{false}, 1001, routing.ErrInvalidParam},
	}
	reg := routing.NewRegistry()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := reg.Define(tc.id, tc.n, tc.back, tc.insp, tc.R)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Define(%q) err=%v, want %v", tc.id, err, tc.wantErr)
			}
			t.Logf("Define(%q, n=%d, R=%d) -> %v", tc.id, tc.n, tc.R, err)
		})
	}
}

func mkBack(n int) []int {
	back := make([]int, n)
	for j := range back {
		back[j] = j + 1
	}
	return back
}

func TestDefineConflictAndGet(t *testing.T) {
	reg := routing.NewRegistry()
	if err := reg.Define("r", 2, []int{1, 2}, []bool{false, true}, 1); err != nil {
		t.Fatalf("first Define: %v", err)
	}
	if err := reg.Define("r", 1, []int{1}, []bool{false}, 0); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("duplicate Define err=%v, want ErrConflict", err)
	}
	if _, err := reg.Get("missing"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("Get missing err=%v, want ErrNotFound", err)
	}
	rt, err := reg.Get("r")
	if err != nil {
		t.Fatalf("Get r: %v", err)
	}
	if rt.N != 2 || rt.R != 1 || rt.Back[1] != 2 || !rt.Insp[1] {
		t.Fatalf("unexpected route: %+v", rt)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	reg := routing.NewRegistry()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := fmt.Sprintf("r-%d-%d", w, j)
				if err := reg.Define(id, 1, []int{1}, []bool{true}, 2); err != nil {
					t.Errorf("Define %s: %v", id, err)
				}
				if _, err := reg.Get(id); err != nil {
					t.Errorf("Get %s: %v", id, err)
				}
			}
		}(w)
	}
	wg.Wait()
}
