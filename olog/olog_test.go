package olog

import (
	"math/rand"
	"strconv"
	"testing"

	"ontology/txn"
)

func mk(seq int, key string, val int64) txn.Rec {
	return txn.Rec{Seq: seq, Key: key, Val: val}
}

func TestExactlyOnce(t *testing.T) {
	cases := []struct {
		tx   string
		recs []txn.Rec
		want int // 本次新追加条数
		log  int // 之后日志总条数
	}{
		{"t1", []txn.Rec{mk(0, "a", 5), mk(1, "b", 3), mk(2, "c", 7)}, 3, 3},
		{"t2", []txn.Rec{mk(0, "a", 9), mk(1, "d", 2)}, 2, 5},
		{"t1", []txn.Rec{mk(3, "e", 4)}, 1, 6},                 // 同 txID 追加新 Seq
		{"t1", []txn.Rec{mk(0, "a", 5), mk(1, "b", 99)}, 0, 6}, // 重放，内容不同也跳过
		{"t2", []txn.Rec{mk(2, "a", 11)}, 1, 7},                // 同 txID 又一新 Seq
		{"t2", []txn.Rec{mk(2, "a", 99)}, 0, 7},                // 再重放
	}
	l := New()
	for i, c := range cases {
		n, err := l.Commit(c.tx, c.recs)
		if err != nil || n != c.want || l.logLen() != c.log {
			t.Fatalf("case %d: n=%d log=%d err=%v, want n=%d log=%d", i, n, l.logLen(), err, c.want, c.log)
		}
	}
	// 日志中每个 (txID,Seq) 恰好出现一次。
	counts := map[txn.RecID]int{}
	l.mu.Lock()
	for _, e := range l.log {
		counts[e.id]++
	}
	l.mu.Unlock()
	for id, c := range counts {
		if c != 1 {
			t.Fatalf("%v appears %d times in log, want exactly once", id, c)
		}
	}
	if len(counts) != 7 {
		t.Fatalf("unique ids=%d, want 7", len(counts))
	}
	if !viewEqual(l.View(), l.recompute()) {
		t.Fatalf("materialized view diverges from batch recompute: %v vs %v", l.View(), l.recompute())
	}
	if v := l.View()["a"]; v != 11 || l.View()["b"] != 3 {
		t.Fatalf("last-write-wins violated: a=%d b=%d", v, l.View()["b"])
	}
}

func TestReplayLookupConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		recs := make([]txn.Rec, m)
		for i := range recs {
			recs[i] = mk(i, "k"+strconv.Itoa(i), int64(i))
		}
		if n, err := l.Commit("bulk", recs); err != nil || n != m {
			t.Fatalf("m=%d: seed commit n=%d err=%v", m, n, err)
		}
		rng := rand.New(rand.NewSource(int64(m)))
		// 随机重放顺序：每条重放只允许常数次哈希探测，不随 m 增长。
		for trial := 0; trial < 25; trial++ {
			pick := recs[rng.Intn(m)]
			if n, _ := l.Commit("bulk", []txn.Rec{pick}); n != 0 {
				t.Fatalf("m=%d: replay appended %d", m, n)
			}
			if got := l.replayScan(); got > lookupBudget {
				t.Fatalf("m=%d: replay lookup=%d > constant %d (linear scan?)", m, got, lookupBudget)
			}
		}
	}
}

func viewEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
