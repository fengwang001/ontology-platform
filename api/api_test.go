package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func TestInvalidEvents(t *testing.T) {
	for i, ev := range []api.Event{
		{Kind: 0, Tx: 1}, {Kind: 5, Tx: 1}, {Kind: 91, Tx: 1},
		{Kind: api.BEGIN, Tx: 0}, {Kind: api.ROW, Tx: -3, Data: "x"},
		{Kind: api.COMMIT, Tx: 0}, {Kind: api.ROLLBACK, Tx: -1},
	} {
		a := api.New(2)
		out, err := a.Feed(ev)
		if !errors.Is(err, api.ErrInvalidEvent) || out != nil || a.Buffered() != 0 {
			t.Fatalf("case %d: err=%v out=%v buf=%d", i, err, out, a.Buffered())
		}
		if _, err := a.Feed(api.Event{Kind: api.BEGIN, Tx: 1}); err != nil {
			t.Fatalf("case %d unusable after rejection: %v", i, err)
		}
	}
}
func TestThirteenSteps(t *testing.T) {
	B, R, C, X := api.BEGIN, api.ROW, api.COMMIT, api.ROLLBACK
	a := api.New(3)
	evs := []api.Event{
		{Kind: B, Tx: 1}, {Kind: B, Tx: 2}, {Kind: R, Tx: 2, Data: "a"}, {Kind: R, Tx: 1, Data: "b"}, {Kind: B, Tx: 3},
		{Kind: R, Tx: 3, Data: "c"}, {Kind: R, Tx: 2, Data: "d"}, {Kind: C, Tx: 2}, {Kind: R, Tx: 1, Data: "e"},
		{Kind: X, Tx: 3}, {Kind: R, Tx: 1, Data: "f"}, {Kind: C, Tx: 1}, {Kind: C, Tx: 3},
	}
	wantErr := []error{nil, nil, nil, nil, nil, nil, api.ErrBufferFull, nil, nil, nil, nil, nil, api.ErrUnknownTxn}
	wantBuf := []int{0, 0, 1, 2, 2, 3, 3, 2, 3, 2, 3, 0, 0}
	for i, ev := range evs {
		out, err := a.Feed(ev)
		wantEmit := wantErr[i] == nil && ev.Kind == C
		gotEmit := len(out) == 1 && out[0].Tx == ev.Tx
		if !errors.Is(err, wantErr[i]) || a.Buffered() != wantBuf[i] || wantEmit != gotEmit {
			t.Fatalf("step %d: err=%v buf=%d emit=%v", i+1, err, a.Buffered(), gotEmit)
		}
	}
	want := []api.Txn{{Tx: 2, Rows: []string{"a"}}, {Tx: 1, Rows: []string{"b", "e", "f"}}}
	if !reflect.DeepEqual(a.Output(), want) {
		t.Fatalf("output=%v want=%v", a.Output(), want)
	}
}

// TestReferenceEquivalence feeds randomized interleavings vs the scan-based reference.
func TestReferenceEquivalence(t *testing.T) {
	kinds := []api.Kind{api.BEGIN, api.ROW, api.ROW, api.ROW, api.COMMIT, api.COMMIT, api.ROLLBACK, 7}
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := api.New(4)
		var log []api.Event
		for i := 0; i < 80; i++ {
			ev := api.Event{Kind: kinds[rng.Intn(8)], Tx: int64(1 + rng.Intn(4)), Data: fmt.Sprintf("%d", i)}
			out, err := a.Feed(ev)
			if err != nil {
				continue
			}
			log = append(log, ev)
			if ev.Kind == api.COMMIT && (len(out) != 1 || out[0].Tx != ev.Tx) {
				t.Fatalf("seed %d: commit emission wrong", seed)
			}
		}
		if out, buf := reference(log); !reflect.DeepEqual(a.Output(), out) || a.Buffered() != buf {
			t.Fatalf("seed %d: out=%v ref=%v buf=%d refbuf=%d", seed, a.Output(), out, a.Buffered(), buf)
		}
	}
}

// reference is the independent naive reference: done marks terminated txs;
// each committed txn is rebuilt by scanning the log prefix; buffered rows
// are exactly the ROWs of never-terminated transactions.
func reference(log []api.Event) ([]api.Txn, int) {
	done := map[int64]bool{}
	out := []api.Txn{}
	for i, e := range log {
		if e.Kind == api.COMMIT || e.Kind == api.ROLLBACK {
			done[e.Tx] = true
		}
		if e.Kind != api.COMMIT {
			continue
		}
		var rows []string
		for _, p := range log[:i] {
			if p.Kind == api.ROW && p.Tx == e.Tx {
				rows = append(rows, p.Data)
			}
		}
		out = append(out, api.Txn{Tx: e.Tx, Rows: rows})
	}
	buf := 0
	for _, e := range log {
		if e.Kind == api.ROW && !done[e.Tx] {
			buf++
		}
	}
	return out, buf
}
func TestSelfCheck(t *testing.T) {
	for _, m := range []int{0, 1, 3, 100} {
		if err := api.New(m).SelfCheck(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConcurrent drives N goroutines, one distinct tx each, without sleeps.
func TestConcurrent(t *testing.T) {
	const N, per = 32, 24
	a := api.New(N * per)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(tx int64) {
			defer wg.Done()
			a.Feed(api.Event{Kind: api.BEGIN, Tx: tx})
			for j := 0; j < per; j++ {
				a.Feed(api.Event{Kind: api.ROW, Tx: tx, Data: fmt.Sprintf("%d:%d", tx, j)})
			}
			a.Feed(api.Event{Kind: api.COMMIT, Tx: tx})
		}(int64(g + 1))
	}
	wg.Wait()
	if len(a.Output()) != N || a.Buffered() != 0 {
		t.Fatalf("out=%d buf=%d want %d,0", len(a.Output()), a.Buffered(), N)
	}
	byTx := map[int64][]string{}
	for _, tx := range a.Output() {
		byTx[tx.Tx] = tx.Rows
	}
	for tx := int64(1); tx <= N; tx++ {
		got := byTx[tx]
		if len(got) != per {
			t.Fatalf("tx %d rows=%d want %d", tx, len(got), per)
		}
		for j, r := range got {
			if w := fmt.Sprintf("%d:%d", tx, j); r != w {
				t.Fatalf("tx %d row %d=%q want %q", tx, j, r, w)
			}
		}
	}
}
