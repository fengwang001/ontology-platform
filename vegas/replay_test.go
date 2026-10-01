package vegas

import (
	"math/rand"
	"testing"
)

// replayRecord 记录一次操作及其可观察结果，用于比较两次重放是否完全一致。
type replayRecord struct {
	op   testOp
	code string
	seq  int64
	w    int64
	exp  int64
	l    int64
	n    int64
	m    int64
}

func runReplay(c Config, ops []testOp) []replayRecord {
	l, err := New(c)
	if err != nil {
		panic(err)
	}
	rec := make([]replayRecord, 0, len(ops))
	for _, op := range ops {
		r := replayRecord{op: op}
		if op.kind == 0 {
			tk, e := l.Acquire(op.now)
			r.code = errCode(e)
			r.seq, r.w, r.exp = tk.Seq, tk.W, tk.ExpiresAt
		} else {
			e := l.Release(op.seq, op.res, op.rtt, op.now)
			r.code = errCode(e)
		}
		r.l, r.n, r.m = l.L(), l.N(), l.MinRTT()
		rec = append(rec, r)
	}
	return rec
}

// TestDeterministicReplay：相同操作序列重放两次（独立实例），
// L 序列、放行判定（错误码、令牌序号/w/超时时刻）、n 与 m 必须完全一致。
func TestDeterministicReplay(t *testing.T) {
	r := rand.New(rand.NewSource(20261001))
	for g := 0; g < 50; g++ {
		c := randConfig(r)
		ops, _ := genOps(r, 200, c)
		a := runReplay(c, ops)
		b := runReplay(c, ops)
		if len(a) != len(b) {
			t.Fatalf("group %d length mismatch", g)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("group %d op %d replay differs:\n first=%+v\n second=%+v", g, i, a[i], b[i])
			}
		}
	}
}
