package olog

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/txn"
)

var eightSteps = []struct {
	tx    string
	recs  []Rec
	added int
}{
	{"t1", []Rec{{Seq: 0, Key: "a", Val: 5}, {Seq: 1, Key: "b", Val: 3}, {Seq: 2, Key: "c", Val: 7}}, 3},
	{"t2", []Rec{{Seq: 0, Key: "a", Val: 9}, {Seq: 1, Key: "d", Val: 2}}, 2},
	{"t1", []Rec{{Seq: 3, Key: "e", Val: 4}}, 1},
	{"t1", []Rec{{Seq: 0, Key: "a", Val: 5}}, 0},
	{"t1", []Rec{{Seq: 1, Key: "b", Val: 99}}, 0},
	{"t3", []Rec{{Seq: 0, Key: "f", Val: 1}, {Seq: 1, Key: "", Val: 2}}, 0},
	{"t2", []Rec{{Seq: 2, Key: "a", Val: 11}}, 1},
	{"t2", []Rec{{Seq: 2, Key: "a", Val: 99}}, 0},
}

// TestLogExactlyOnce 钉住 I2：每个 (txID,Seq) 在日志中恰好一次，改值重放不追加。
func TestLogExactlyOnce(t *testing.T) {
	l := New()
	for i, s := range eightSteps {
		n, err := l.Commit(s.tx, s.recs)
		if i == 5 {
			if !errors.Is(err, txn.ErrEmptyKey) {
				t.Fatalf("step6 err=%v want ErrEmptyKey", err)
			}
			continue
		}
		if err != nil || n != s.added {
			t.Fatalf("step%d added=%d err=%v want %d", i+1, n, err, s.added)
		}
	}
	count := map[idKey]int{}
	for _, e := range l.log {
		k := idKey{e.txID, e.rec.Seq}
		if count[k]++; count[k] > 1 {
			t.Fatalf("%v logged more than once", k)
		}
	}
	if len(l.log) != 7 || len(l.done) != 7 {
		t.Fatalf("entries=%d done=%d, want both 7", len(l.log), len(l.done))
	}
	for k := range l.done {
		if n, err := l.Commit(k.txID, []Rec{{Seq: k.seq, Key: "zz", Val: -1}}); err != nil || n != 0 {
			t.Fatalf("replay %v: added=%d err=%v", k, n, err)
		}
	}
	if len(l.log) != 7 {
		t.Fatal("replay appended entries")
	}
}

// TestRejectAtomic 钉住 I4：三类拒绝可判定、两两互异，日志/视图/已提交集合一律不变。
func TestRejectAtomic(t *testing.T) {
	cases := []struct {
		tx   string
		recs []Rec
		want error
	}{
		{"", []Rec{{Seq: 0, Key: "a", Val: 1}}, txn.ErrEmptyTxID},
		{"t", []Rec{{Seq: 0, Key: "a", Val: 1}, {Seq: 1, Key: "", Val: 2}}, txn.ErrEmptyKey},
		{"t", []Rec{{Seq: 0, Key: "a", Val: 1}, {Seq: 0, Key: "b", Val: 2}}, txn.ErrDupSeq},
	}
	for _, c := range cases {
		l := New()
		l.Commit("seed", []Rec{{Seq: 9, Key: "z", Val: 5}})
		la, va, da := len(l.log), len(l.view), len(l.done)
		if n, err := l.Commit(c.tx, c.recs); n != 0 || !errors.Is(err, c.want) ||
			len(l.log) != la || len(l.view) != va || len(l.done) != da {
			t.Fatalf("reject %v: added=%d err=%v or state changed", c.want, n, err)
		}
	}
	if txn.ErrEmptyTxID == txn.ErrEmptyKey || txn.ErrEmptyKey == txn.ErrDupSeq ||
		txn.ErrEmptyTxID == txn.ErrDupSeq {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

func TestProbeConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l, recs := New(), make([]Rec, m)
		for i := range recs {
			recs[i] = Rec{Seq: i, Key: fmt.Sprintf("k%d", i), Val: int64(i)}
		}
		if n, _ := l.Commit("bulk", recs); n != m {
			t.Fatalf("seed added=%d want %d", n, m)
		}
		if n, _ := l.Commit("bulk", recs[:1]); n != 0 || l.probeScan != 1 {
			t.Fatalf("m=%d replay1 probes=%d want 1", m, l.probeScan)
		}
		if n, _ := l.Commit("bulk", []Rec{recs[1], recs[m/2], recs[m-1]}); n != 0 || l.probeScan != 3 {
			t.Fatalf("m=%d replay3 probes=%d want 3", m, l.probeScan)
		}
	}
}

// TestConcurrent 不同 txID 并发提交，并发读者只见合法快照（无撕裂）；终态等于批量重算。无 sleep。
func TestConcurrent(t *testing.T) {
	const N, per = 64, 50
	l, stop := New(), make(chan struct{})
	var torn atomic.Bool
	var wr, ww sync.WaitGroup
	wr.Add(1)
	go func() {
		defer wr.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if v := l.View()["shared"]; v != 0 && v != 42 {
					torn.Store(true)
				}
			}
		}
	}()
	for g := 0; g < N; g++ {
		ww.Add(1)
		go func(g int) {
			defer ww.Done()
			recs := make([]Rec, per)
			for j := range recs {
				k, v := "shared", int64(42)
				if j > 0 {
					k, v = fmt.Sprintf("g%d-k%d", g, j), int64(g*per+j)
				}
				recs[j] = Rec{Seq: j, Key: k, Val: v}
			}
			if n, err := l.Commit(fmt.Sprintf("g%d", g), recs); err != nil || n != per {
				t.Errorf("g%d: added=%d err=%v", g, n, err)
			}
		}(g)
	}
	ww.Wait()
	close(stop)
	wr.Wait()
	if torn.Load() || len(l.View()) != 1+N*(per-1) || l.View()["shared"] != 42 {
		t.Fatal("torn view or final != batch recompute")
	}
}
