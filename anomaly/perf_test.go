package anomaly

import "testing"

func BenchmarkComplete12(b *testing.B) {
	n := 12
	txns := make([]Txn, n)
	order := map[string][]Version{}
	for i := range txns {
		id := i + 1
		txns[i] = Txn{ID: id, Status: Committed, Ops: []Op{{Key: "k1"}, {Key: "kn"}}}
	}
	// k1：1,2,...,n（仅相邻产生 ww）；kn：n,...,1。
	// 再用 n-2 个键为其余每对事务补一条方向的 ww，构造完全有向 ww 图。
	up := []Version{{0, 0}}
	down := []Version{{0, 0}}
	for i := 0; i < n; i++ {
		up = append(up, Version{Txn: i + 1, Seq: 1})
		down = append(down, Version{Txn: n - i, Seq: 1})
	}
	order["k1"] = up
	order["kn"] = down
	keyIdx := 0
	for a := 1; a <= n; a++ {
		for b := 1; b <= n; b++ {
			if b == a+1 || b == a-1 {
				continue
			}
			if a == b {
				continue
			}
			keyIdx++
			name := "p" + string(rune('a'+keyIdx))
			order[name] = []Version{{0, 0}, {Txn: a, Seq: 1}, {Txn: b, Seq: 1}}
			txns[a-1].Ops = append(txns[a-1].Ops, Op{Key: name})
			txns[b-1].Ops = append(txns[b-1].Ops, Op{Key: name})
		}
	}
	h := History{Txns: txns, Order: order}
	if r := Analyze(h); r.Rejected {
		b.Fatalf("benchmark history rejected: %v", r.Err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := Analyze(h)
		if r.Category != G0 {
			b.Fatalf("want G0 got %s", r.Category)
		}
	}
}
