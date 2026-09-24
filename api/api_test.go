package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"reflect"
	"sync"
	"testing"
)

func eight() []api.Txn {
	w := [][]string{{"a"}, {"b"}, {"a", "c"}, {"d"}, {"b", "d"}, {"c"}, {"e"}, {"b", "e"}}
	rd := [][]string{nil, {"a"}, nil, {"c"}, nil, {"b"}, {"a"}, nil}
	tx := make([]api.Txn, 8)
	for i := range tx {
		tx[i] = api.Txn{Seq: int64(i + 1), Writes: w[i], Reads: rd[i]}
	}
	return tx
}
func serial(ws [][]string) map[string]int64 {
	m := map[string]int64{}
	for i, w := range ws {
		for _, k := range w {
			m[k] = int64(i + 1)
		}
	}
	return m
}
func mkEngine(t *testing.T, seed int64, n, keys, p int) (*api.Engine, [][]string) {
	t.Helper()
	e, _ := api.New(p, n+1)
	rng := rand.New(rand.NewSource(seed))
	ws := make([][]string, n)
	for i := range ws {
		ws[i] = []string{fmt.Sprintf("k%d", rng.Intn(keys))}
		if err := e.Append([]api.Txn{{Seq: int64(i + 1), Writes: ws[i]}}); err != nil {
			t.Fatal(err)
		}
	}
	return e, ws
}
func TestEightTxnSpec(t *testing.T) {
	e, _ := api.New(2, 64)
	if err := e.Append(eight()); err != nil {
		t.Fatal(err)
	}
	wd, wr, pl := []int{1, 1, 2, 1, 2, 3, 1, 3}, []int{1, 1, 2, 2, 3, 3, 4, 5}, e.Rounds()
	for i := 1; i <= 8; i++ {
		d, _ := e.Depth(int64(i))
		if d != wd[i-1] || pl.RoundOf[i-1] != wr[i-1] {
			t.Fatalf("seq %d depth/round wrong: %d %d", i, d, pl.RoundOf[i-1])
		}
	}
	wantKV := map[string]int64{"a": 3, "b": 8, "c": 6, "d": 5, "e": 8}
	if pl.Total != 5 || e.MaxParallel() != 4 || !reflect.DeepEqual(e.Replay(), wantKV) {
		t.Fatalf("total=%d maxpar=%d kv=%v", pl.Total, e.MaxParallel(), e.Replay())
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestRandomInvariants pins invariants 2 and 3 on random sequences/caps.
func TestScheduleLegalAndReplay(t *testing.T) {
	for seed := int64(0); seed < 80; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n, p, keys := 1+rng.Intn(50), 1+rng.Intn(4), 1+rng.Intn(6)
		e, ws := mkEngine(t, seed, n, keys, p)
		pl := e.Rounds()
		width, last := map[int]int{}, map[string]int{}
		for i := 0; i < n; i++ {
			r := pl.RoundOf[i]
			if r <= 0 {
				t.Fatalf("seed %d seq %d unscheduled", seed, i+1)
			}
			width[r]++
			for _, k := range ws[i] { // same-key writers must have strictly rising rounds
				if last[k] >= r {
					t.Fatalf("seed %d key %s not strictly ordered at seq %d", seed, k, i+1)
				}
				last[k] = r
			}
		}
		for r, w := range width {
			if w > p {
				t.Fatalf("seed %d round %d width %d > P %d", seed, r, w, p)
			}
		}
		if len(width) != pl.Total || !reflect.DeepEqual(e.Replay(), serial(ws)) {
			t.Fatalf("seed %d round count or replay mismatch", seed)
		}
	}
}

// TestConcurrentReplay pins concurrent field-wise equality (no sleeps).
func TestConcurrentReplay(t *testing.T) {
	e, ws := mkEngine(t, 1, 300, 10, 4)
	wantKV, wantR := serial(ws), e.Rounds().RoundOf
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(e.Replay(), wantKV) || !reflect.DeepEqual(e.Rounds().RoundOf, wantR) {
				t.Errorf("concurrent replay/rounds differ")
			}
		}()
	}
	wg.Wait()
}

// TestAppendRejectionAtomic pins invariant 4 and distinct sentinels.
func TestAppendRejectionAtomic(t *testing.T) {
	if len(map[error]int{api.ErrBadParams: 0, api.ErrSeqGap: 1, api.ErrInvalidTxn: 2, api.ErrCapacity: 3}) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
	if _, err := api.New(0, 1); !errors.Is(err, api.ErrBadParams) {
		t.Fatalf("New=%v want ErrBadParams", err)
	}
	e, _ := api.New(2, 4)
	if err := e.Append([]api.Txn{{Seq: 1, Writes: []string{"a"}}, {Seq: 2, Writes: []string{"b"}}}); err != nil {
		t.Fatal(err)
	}
	snap := e.Rounds().RoundOf
	cases := []struct {
		b    []api.Txn
		want error
	}{
		{[]api.Txn{{Seq: 3, Writes: []string{"a"}}, {Seq: 5, Writes: []string{"c"}}}, api.ErrSeqGap},
		{[]api.Txn{{Seq: 3, Writes: nil}}, api.ErrInvalidTxn},
		{[]api.Txn{{Seq: 3, Writes: []string{"", "x"}}}, api.ErrInvalidTxn},
		{[]api.Txn{{Seq: 3, Writes: []string{"a"}}, {Seq: 4, Writes: []string{"b"}}, {Seq: 5, Writes: []string{"c"}}}, api.ErrCapacity},
	}
	for _, c := range cases {
		if err := e.Append(c.b); !errors.Is(err, c.want) {
			t.Fatalf("err=%v want %v", err, c.want)
		}
		if !reflect.DeepEqual(e.Rounds().RoundOf, snap) {
			t.Fatal("rejected batch changed schedule")
		}
	}
	if err := e.Append([]api.Txn{{Seq: 3, Writes: []string{"c"}}}); err != nil {
		t.Fatalf("engine unusable after rejection: %v", err)
	}
	if d, ok := e.Depth(3); !ok || d != 1 {
		t.Fatalf("seq3 depth=%d,%v want 1,true", d, ok)
	}
}
