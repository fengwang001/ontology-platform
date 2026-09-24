// Package api is the thread-safe public entry point of the CDC regrouper.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"

	"ontology/regroup"
	"ontology/txn"
)

type (
	Event = txn.Event
	Txn   = txn.Txn
	Kind  = txn.Kind
)

const (
	BEGIN    = txn.BEGIN
	ROW      = txn.ROW
	COMMIT   = txn.COMMIT
	ROLLBACK = txn.ROLLBACK
)

var (
	ErrInvalidEvent   = errors.New("invalid CDC event")
	ErrDuplicateBegin = txn.ErrDuplicateBegin
	ErrUnknownTxn     = regroup.ErrUnknownTxn
	ErrBufferFull     = regroup.ErrBufferFull
)
var randKinds = [11]Kind{BEGIN, BEGIN, ROW, ROW, ROW, ROW, COMMIT, COMMIT, COMMIT, ROLLBACK, Kind(91)}

type API struct{ r *regroup.Regrouper }

func New(maxRows int) *API { return &API{r: regroup.New(maxRows)} }

// Feed applies one event atomically. Validation order: illegal event,
// then duplicate BEGIN / unknown txn, then buffer full; rejection mutates nothing.
func (a *API) Feed(ev Event) ([]Txn, error) {
	if ev.Kind < BEGIN || ev.Kind > ROLLBACK || ev.Tx <= 0 {
		return nil, ErrInvalidEvent
	}
	switch ev.Kind {
	case BEGIN:
		return nil, a.r.Begin(ev.Tx)
	case ROW:
		return nil, a.r.Row(ev.Tx, ev.Data)
	case ROLLBACK:
		return nil, a.r.Rollback(ev.Tx)
	default:
		t, err := a.r.Commit(ev.Tx)
		if err != nil {
			return nil, err
		}
		return []Txn{t}, nil
	}
}
func (a *API) Output() []Txn { return a.r.Output() }
func (a *API) Buffered() int { return a.r.Buffered() }

// SelfCheck replays the 13-step case and randomized interleavings,
// checking all four invariants against a scan-based naive reference.
func (a *API) SelfCheck() error {
	if err := thirteen(); err != nil {
		return err
	}
	for s := int64(0); s < 16; s++ {
		got := New(6)
		var log []Event
		for _, ev := range genEvents(rand.New(rand.NewSource(s))) {
			out, err := got.Feed(ev)
			if err != nil {
				continue
			}
			log = append(log, ev)
			if (ev.Kind == COMMIT) != (len(out) == 1 && out[0].Tx == ev.Tx) {
				return errors.New("selfcheck: emission mismatch")
			}
		}
		want, sum := naive(log)
		if !reflect.DeepEqual(got.Output(), want) || got.Buffered() != sum {
			return errors.New("selfcheck: reference/ledger mismatch")
		}
	}
	return nil
}

// thirteen replays the NOTES.md 13-step derivation (maxRows=3).
func thirteen() error {
	a := New(3)
	evs := []Event{
		{Kind: BEGIN, Tx: 1}, {Kind: BEGIN, Tx: 2}, {Kind: ROW, Tx: 2, Data: "a"},
		{Kind: ROW, Tx: 1, Data: "b"}, {Kind: BEGIN, Tx: 3}, {Kind: ROW, Tx: 3, Data: "c"},
		{Kind: ROW, Tx: 2, Data: "d"}, {Kind: COMMIT, Tx: 2}, {Kind: ROW, Tx: 1, Data: "e"},
		{Kind: ROLLBACK, Tx: 3}, {Kind: ROW, Tx: 1, Data: "f"}, {Kind: COMMIT, Tx: 1},
		{Kind: COMMIT, Tx: 3},
	}
	errs := []error{nil, nil, nil, nil, nil, nil, ErrBufferFull, nil, nil, nil, nil, nil, ErrUnknownTxn}
	bufs := []int{0, 0, 1, 2, 2, 3, 3, 2, 3, 2, 3, 0, 0}
	for i, ev := range evs {
		out, err := a.Feed(ev)
		commit := errs[i] == nil && ev.Kind == COMMIT
		if !errors.Is(err, errs[i]) || a.Buffered() != bufs[i] ||
			commit != (len(out) == 1 && out[0].Tx == ev.Tx) {
			return fmt.Errorf("step %d: err=%v buf=%d", i+1, err, a.Buffered())
		}
	}
	want := []Txn{{Tx: 2, Rows: []string{"a"}}, {Tx: 1, Rows: []string{"b", "e", "f"}}}
	if !reflect.DeepEqual(a.Output(), want) {
		return errors.New("final output mismatch")
	}
	return nil
}
func genEvents(rng *rand.Rand) []Event {
	evs := make([]Event, 0, 96)
	for i := 0; i < 96; i++ {
		evs = append(evs, Event{Kind: randKinds[rng.Intn(11)], Tx: int64(1 + rng.Intn(5)), Data: fmt.Sprintf("r%d", i)})
	}
	return evs
}

// naive is the independent reference over the accepted-event log: each
// accepted COMMIT scans the log prefix for that tx's rows (arrival order).
func naive(log []Event) ([]Txn, int) {
	var want []Txn
	n, sum := map[int64]int{}, 0
	for i, e := range log {
		switch e.Kind {
		case ROW:
			n[e.Tx]++
			sum++
		case COMMIT:
			sum -= n[e.Tx]
			n[e.Tx] = 0
			t := Txn{Tx: e.Tx}
			for _, p := range log[:i] {
				if p.Kind == ROW && p.Tx == e.Tx {
					t.Rows = append(t.Rows, p.Data)
				}
			}
			want = append(want, t)
		case ROLLBACK:
			sum -= n[e.Tx]
			n[e.Tx] = 0
		}
	}
	return want, sum
}
