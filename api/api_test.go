package api

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/txn"
)

func randSeq(r *rand.Rand, n int) []Event {
	kinds := []txn.Kind{Begin, Commit, Rollback, Row, Row}
	var seq []Event
	for i := 0; i < n; i++ {
		seq = append(seq, ev(kinds[r.Intn(5)], int64(r.Intn(4)+1), fmt.Sprintf("d%d", i)))
		if r.Intn(20) == 0 {
			seq = append(seq, Event{Kind: 42, Tx: 1}) // 非法事件
		}
	}
	return seq
}
func checkVsNaive(t *testing.T, seq []Event, maxRows int) {
	t.Helper()
	p := New(maxRows)
	for _, e := range seq {
		p.Feed(e)
	}
	got, want := p.Output(), naive(seq, maxRows)
	eq := func(a, b Txn) bool { return a.Tx == b.Tx && slices.Equal(a.Rows, b.Rows) }
	if !slices.EqualFunc(got, want, eq) {
		t.Fatalf("maxRows=%d: got %v want %v", maxRows, got, want)
	}
}
func TestNaiveConsistency(t *testing.T) {
	thirteen := []Event{ev(Begin, 1, ""), ev(Begin, 2, ""), ev(Row, 2, "a"), ev(Row, 1, "b"),
		ev(Begin, 3, ""), ev(Row, 3, "c"), ev(Row, 2, "d"), ev(Commit, 2, ""),
		ev(Row, 1, "e"), ev(Rollback, 3, ""), ev(Row, 1, "f"), ev(Commit, 1, ""), ev(Commit, 3, "")}
	for _, maxRows := range []int{1, 3, 7, 50} {
		checkVsNaive(t, thirteen, maxRows)
		for seed := 0; seed < 100; seed++ {
			checkVsNaive(t, randSeq(rand.New(rand.NewSource(int64(seed))), 60), maxRows)
		}
	}
}
func TestAtomicity(t *testing.T) {
	for seed := 0; seed < 100; seed++ {
		p, seq := New(5), randSeq(rand.New(rand.NewSource(int64(seed))), 80)
		rows, rolled, seen := map[int64][]string{}, map[int64]bool{}, map[int64]bool{}
		for _, e := range seq {
			if _, err := p.Feed(e); err != nil {
				continue
			}
			if e.Kind == Row {
				rows[e.Tx] = append(rows[e.Tx], e.Data)
			} else if e.Kind == Rollback {
				rolled[e.Tx] = true
			}
		}
		for _, out := range p.Output() {
			if rolled[out.Tx] || seen[out.Tx] || !slices.Equal(out.Rows, rows[out.Tx]) {
				t.Fatalf("tx %d: rolled=%v seen=%v rows=%v want %v",
					out.Tx, rolled[out.Tx], seen[out.Tx], out.Rows, rows[out.Tx])
			}
			seen[out.Tx] = true
		}
	}
}
func TestBufferedAccounting(t *testing.T) {
	for _, maxRows := range []int{0, 1, 3, 9} {
		for seed := 0; seed < 50; seed++ {
			p := New(maxRows)
			open, total := map[int64]int{}, 0
			for _, e := range randSeq(rand.New(rand.NewSource(int64(seed))), 60) {
				if _, err := p.Feed(e); err == nil {
					if e.Kind == Row {
						open[e.Tx]++
						total++
					} else if e.Kind == Commit || e.Kind == Rollback {
						total -= open[e.Tx]
						delete(open, e.Tx)
					}
				}
				if p.Buffered() != total || p.Buffered() > maxRows {
					t.Fatalf("maxRows=%d: Buffered()=%d, model=%d", maxRows, p.Buffered(), total)
				}
			}
		}
	}
}
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		bad  Event
		want error
	}{
		{Event{Kind: 42, Tx: 1}, ErrInvalidEvent},
		{ev(Row, 0, "x"), ErrInvalidEvent},
		{ev(Begin, 1, ""), ErrDuplicateBegin},
		{ev(Row, 9, "x"), ErrUnknownTx},
		{ev(Commit, 9, ""), ErrUnknownTx},
		{ev(Rollback, 9, ""), ErrUnknownTx},
		{ev(Row, 1, "overflow"), ErrBufferFull},
	}
	for _, c := range cases {
		p := New(1)
		p.Feed(ev(Begin, 1, ""))
		p.Feed(ev(Row, 1, "a"))
		outBefore, bufBefore := fmt.Sprint(p.Output()), p.Buffered()
		if _, err := p.Feed(c.bad); err != c.want {
			t.Fatalf("bad=%+v: err=%v want %v", c.bad, err, c.want)
		}
		if p.Buffered() != bufBefore || fmt.Sprint(p.Output()) != outBefore {
			t.Fatalf("bad=%+v changed state", c.bad)
		}
		if _, err := p.Feed(ev(Commit, 1, "")); err != nil { // 拒绝后仍可正常使用
			t.Fatalf("bad=%+v: subsequent commit failed: %v", c.bad, err)
		}
	}
	if len(map[error]bool{ErrInvalidEvent: true, ErrDuplicateBegin: true, ErrUnknownTx: true, ErrBufferFull: true}) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestConcurrentFeeds(t *testing.T) {
	const n, rowsPer = 64, 10
	p := New(1 << 20)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(tx int64) {
			defer wg.Done()
			p.Feed(ev(Begin, tx, ""))
			for j := 0; j < rowsPer; j++ {
				p.Feed(ev(Row, tx, fmt.Sprintf("t%d-r%d", tx, j)))
			}
			p.Feed(ev(Commit, tx, ""))
		}(int64(g + 1))
	}
	wg.Wait()
	if len(p.Output()) != n || p.Buffered() != 0 {
		t.Fatalf("got %d txns, buffered=%d", len(p.Output()), p.Buffered())
	}
	for _, o := range p.Output() {
		for j := 0; j < rowsPer; j++ {
			if o.Rows[j] != fmt.Sprintf("t%d-r%d", o.Tx, j) {
				t.Fatalf("tx %d row %d = %q", o.Tx, j, o.Rows[j])
			}
		}
	}
}
