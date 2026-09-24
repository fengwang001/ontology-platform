package hist

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestOrderMatchesBatch(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42} {
		if _, err := genHist(seed, 200); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClockConditionAllPairs(t *testing.T) {
	for _, seed := range []int64{7, 11, 99} {
		h, err := genHist(seed, 150)
		if err != nil {
			t.Fatal(err)
		}
		for a := 1; a < len(h.byID); a++ {
			for b, ok := range h.closure(a) {
				if ok && a != b && h.byID[a].TS >= h.byID[b].TS {
					t.Fatalf("seed=%d: e%d->e%d ts %d>=%d", seed, a, b, h.byID[a].TS, h.byID[b].TS)
				}
			}
		}
	}
}

func TestStrictTimestamps(t *testing.T) {
	for _, seed := range []int64{4, 5, 6} {
		h, err := genHist(seed, 120)
		if err != nil {
			t.Fatal(err)
		}
		for node := 1; node <= h.n; node++ {
			var prev int64
			for _, id := range h.chains[node-1] {
				ts := h.byID[id].TS
				if ts <= prev {
					t.Fatalf("seed=%d node=%d ts=%d <= %d", seed, node, ts, prev)
				}
				prev = ts
			}
		}
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	if len(map[error]bool{ErrNode: true, ErrNoMessage: true, ErrAlreadyRecv: true, ErrLimit: true}) != 4 {
		t.Fatal("sentinels must be distinct")
	}
	for _, bad := range [][2]int{{0, 10}, {2, 0}} {
		if _, e := New(bad[0], bad[1]); !errors.Is(e, ErrNode) {
			t.Fatalf("New%v err=%v", bad, e)
		}
	}
	h, _ := New(3, 100)
	_, id, _ := h.Send(1, 2)
	h.Recv(id)
	ops := [][3]int{{0, 0, 0}, {0, 4, 0}, {1, 0, 1}, {1, 1, 4}, {2, 99, 0}, {2, 1, 0}}
	wants := []error{ErrNode, ErrNode, ErrNode, ErrNode, ErrNoMessage, ErrAlreadyRecv}
	for i, op := range ops {
		before := state(h)
		var err error
		switch op[0] {
		case 0:
			_, err = h.Local(op[1])
		case 1:
			_, _, err = h.Send(op[1], op[2])
		default:
			_, err = h.Recv(op[1])
		}
		if !errors.Is(err, wants[i]) {
			t.Fatalf("op%v: err=%v want %v", op, err, wants[i])
		}
		if !reflect.DeepEqual(before, state(h)) {
			t.Fatalf("op%v: state changed", op)
		}
	}
	if _, e := h.Local(1); e != nil {
		t.Fatalf("unusable after rejections: %v", e)
	}
	lim, _ := New(1, 1)
	lim.Local(1)
	before := state(lim)
	if _, e := lim.Local(1); !errors.Is(e, ErrLimit) || !reflect.DeepEqual(before, state(lim)) {
		t.Fatal("limit rejection violated")
	}
}

func TestInsertComparisonsLogarithmic(t *testing.T) {
	if err := CheckInsertBound(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		h, _ := New(3, m+1)
		for i := 0; i < m/2; i++ {
			h.Local(3)
		}
		for node := 1; h.nextID < m; node = 3 - node {
			h.Local(node)
		}
		h.Local(1)
		if bound := 2*ceilLog2(m+1) + 2; h.cmpCount > bound {
			t.Fatalf("m=%d comparisons=%d > bound=%d", m, h.cmpCount, bound)
		}
	}
}

func TestConcurrentLocalsAndReaders(t *testing.T) {
	for _, nk := range [][2]int{{2, 100}, {8, 500}} {
		n, K := nk[0], nk[1]
		h, _ := New(n, n*K)
		var wg sync.WaitGroup
		for node := 1; node <= n; node++ {
			wg.Go(func() {
				for i := 0; i < K; i++ {
					if _, e := h.Local(node); e != nil {
						t.Error(e)
					}
				}
			})
		}
		wg.Wait()
		if got := h.Order(); len(got) != n*K || !reflect.DeepEqual(got, batchSort(h)) {
			t.Fatalf("n=%d K=%d order wrong", n, K)
		}
		for node := 1; node <= n; node++ {
			for i, id := range h.chains[node-1] {
				if h.byID[id].TS != int64(i+1) {
					t.Fatalf("node=%d pos=%d ts=%d", node, i, h.byID[id].TS)
				}
			}
		}
		first := h.Order()
		for i := 0; i < 8; i++ {
			wg.Go(func() {
				if !reflect.DeepEqual(first, h.Order()) {
					t.Error("readers disagree")
				}
			})
		}
		wg.Wait()
	}
}
