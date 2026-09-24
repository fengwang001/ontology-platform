// Command demo exercises the CDC transaction regrouper. No args, no network.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK:", name)
	} else {
		fails++
		fmt.Println("FAIL:", name)
	}
}

func main() {
	// 13-step derivation (maxRows=3): per-step verdict, output, Buffered.
	a := api.New(3)
	evs := []api.Event{
		{Kind: api.BEGIN, Tx: 1}, {Kind: api.BEGIN, Tx: 2}, {Kind: api.ROW, Tx: 2, Data: "a"},
		{Kind: api.ROW, Tx: 1, Data: "b"}, {Kind: api.BEGIN, Tx: 3}, {Kind: api.ROW, Tx: 3, Data: "c"},
		{Kind: api.ROW, Tx: 2, Data: "d"}, {Kind: api.COMMIT, Tx: 2}, {Kind: api.ROW, Tx: 1, Data: "e"},
		{Kind: api.ROLLBACK, Tx: 3}, {Kind: api.ROW, Tx: 1, Data: "f"}, {Kind: api.COMMIT, Tx: 1},
		{Kind: api.COMMIT, Tx: 3},
	}
	wantErr := []error{nil, nil, nil, nil, nil, nil, api.ErrBufferFull, nil, nil, nil, nil, nil, api.ErrUnknownTxn}
	wantBuf := []int{0, 0, 1, 2, 2, 3, 3, 2, 3, 2, 3, 0, 0}
	stepsOK := true
	for i, ev := range evs {
		out, err := a.Feed(ev)
		emitted := ev.Kind == api.COMMIT && wantErr[i] == nil
		if !errors.Is(err, wantErr[i]) || a.Buffered() != wantBuf[i] ||
			emitted != (len(out) == 1 && out[0].Tx == ev.Tx) {
			stepsOK = false
		}
	}
	final := []api.Txn{{Tx: 2, Rows: []string{"a"}}, {Tx: 1, Rows: []string{"b", "e", "f"}}}
	check("13 steps verdict/output/Buffered", stepsOK && eq(a.Output(), final))
	check("COMMIT order kept, rollback rows absent", a.Output()[0].Tx == 2 && a.Output()[1].Tx == 1 &&
		!contains(a.Output(), "c") && !contains(a.Output(), "d"))
	check("matches naive reference", a.SelfCheck() == nil)

	// Four distinct decidable error classes.
	four := errors.Is(mustErr(api.New(1).Feed(api.Event{Kind: api.Kind(9), Tx: 1})), api.ErrInvalidEvent) &&
		errors.Is(mustErr(func() ([]api.Txn, error) {
			d := api.New(1)
			d.Feed(api.Event{Kind: api.BEGIN, Tx: 1})
			return d.Feed(api.Event{Kind: api.BEGIN, Tx: 1})
		}()), api.ErrDuplicateBegin) &&
		errors.Is(mustErr(api.New(1).Feed(api.Event{Kind: api.COMMIT, Tx: 9})), api.ErrUnknownTxn) &&
		errors.Is(mustErr(func() ([]api.Txn, error) {
			d := api.New(0)
			d.Feed(api.Event{Kind: api.BEGIN, Tx: 1})
			return d.Feed(api.Event{Kind: api.ROW, Tx: 1, Data: "x"})
		}()), api.ErrBufferFull)
	check("four distinct sentinel errors", four)

	// A rejected event leaves every observable state untouched.
	d := api.New(1)
	d.Feed(api.Event{Kind: api.BEGIN, Tx: 1})
	d.Feed(api.Event{Kind: api.ROW, Tx: 1, Data: "keep"})
	beforeB, beforeO := d.Buffered(), len(d.Output())
	_, fullErr := d.Feed(api.Event{Kind: api.ROW, Tx: 1, Data: "drop"})
	rejected := errors.Is(fullErr, api.ErrBufferFull) && d.Buffered() == beforeB && len(d.Output()) == beforeO
	_, commitErr := d.Feed(api.Event{Kind: api.COMMIT, Tx: 1})
	check("rejection leaves no trace", rejected && commitErr == nil &&
		eq(d.Output(), []api.Txn{{Tx: 1, Rows: []string{"keep"}}}))

	// Large m per-transaction buffering: a 1-row tx commits independently
	// of a 10000-row in-progress tx without scanning its rows.
	big := api.New(20000)
	big.Feed(api.Event{Kind: api.BEGIN, Tx: 1})
	for i := 0; i < 10000; i++ {
		big.Feed(api.Event{Kind: api.ROW, Tx: 1, Data: "m"})
	}
	big.Feed(api.Event{Kind: api.BEGIN, Tx: 2})
	big.Feed(api.Event{Kind: api.ROW, Tx: 2, Data: "solo"})
	o2, _ := big.Feed(api.Event{Kind: api.COMMIT, Tx: 2})
	check("large m: small txn commits independently", len(o2) == 1 && len(o2[0].Rows) == 1 && big.Buffered() == 10000)

	// Concurrency: N goroutines, one distinct tx each; exact rows, buffer 0.
	const N = 64
	c := api.New(N * 32)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(tx int64) {
			defer wg.Done()
			c.Feed(api.Event{Kind: api.BEGIN, Tx: tx})
			for j := 0; j < 16; j++ {
				c.Feed(api.Event{Kind: api.ROW, Tx: tx, Data: fmt.Sprintf("%d-%d", tx, j)})
			}
			c.Feed(api.Event{Kind: api.COMMIT, Tx: tx})
		}(int64(g + 1))
	}
	wg.Wait()
	okConc := len(c.Output()) == N && c.Buffered() == 0
	for _, t := range c.Output() {
		for j := 0; j < 16 && okConc; j++ {
			okConc = t.Rows[j] == fmt.Sprintf("%d-%d", t.Tx, j)
		}
	}
	check("concurrent N txns exact rows, buffer 0", okConc)
	check("SelfCheck passes", api.New(0).SelfCheck() == nil)

	if fails != 0 {
		os.Exit(1)
	}
}

func mustErr(_ []api.Txn, err error) error { return err }

func eq(got, want []api.Txn) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Tx != want[i].Tx || len(got[i].Rows) != len(want[i].Rows) {
			return false
		}
		for j := range got[i].Rows {
			if got[i].Rows[j] != want[i].Rows[j] {
				return false
			}
		}
	}
	return true
}

func contains(ts []api.Txn, row string) bool {
	for _, t := range ts {
		for _, r := range t.Rows {
			if r == row {
				return true
			}
		}
	}
	return false
}
