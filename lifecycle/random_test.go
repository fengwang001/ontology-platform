package lifecycle

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRandomVsNaive：2000 组随机操作序列，生产实现必须与朴素模拟逐项一致；
// 被拒绝操作前后状态不变、任意时刻已用额度不超过 Q；日志打印输入、输出与判定依据。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 2000
	r := rand.New(rand.NewSource(20261002))

	for seq := 0; seq < sequences; seq++ {
		cfg := randomConfig(r)
		b, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: config rejected: %+v: %v", seq, cfg, err)
		}
		nv := newNaive(cfg)
		ops := randomOps(r, 30+r.Intn(40))

		t.Logf("=== seq %d cfg={p0=%d p1=%d p2=%d A=%d B=%d m1=%d m2=%d r1=%d r2=%d Q=%d}",
			seq, cfg.P0, cfg.P1, cfg.P2, cfg.A, cfg.B, cfg.M1, cfg.M2, cfg.R1, cfg.R2, cfg.Q)

		for step, o := range ops {
			before := snapshot(b)

			var (
				got  []Fees
				gErr error
			)
			switch o.kind {
			case 0:
				f, e := b.Put(o.key, o.size, o.now)
				got, gErr = []Fees{f}, e
			case 1:
				f, e := b.Delete(o.key, o.now)
				got, gErr = []Fees{f}, e
			case 2:
				f, e := b.Get(o.key, o.now)
				got, gErr = []Fees{f}, e
			case 3:
				got, gErr = b.GetMany(o.keys, o.now)
			}
			want, wErr := runNaive(nv, o)

			verdict := "accepted:fees-and-state-match"
			if gErr != nil {
				verdict = "rejected:" + errName(gErr) + ":state-unchanged"
			}
			t.Logf("seq %d step %d | 输入 %s key=%q size=%d now=%d keys=%v argsOK=%v | 输出 err=%v fees=%s | 朴素 err=%v fees=%s | 判定=%s",
				seq, step, kindName(o.kind), o.key, o.size, o.now, o.keys, validRandomArgs(o),
				gErr, fmtFees(got), wErr, fmtFees(want), verdict)

			if !sameErr(gErr, wErr) {
				t.Fatalf("seq %d step %d: error mismatch: got %v want %v",
					seq, step, gErr, wErr)
			}
			if !feeListEqual(got, want) {
				t.Fatalf("seq %d step %d: fees mismatch:\ngot  %s\nwant %s",
					seq, step, fmtFees(got), fmtFees(want))
			}
			if gErr != nil {
				if after := snapshot(b); after != before {
					t.Fatalf("seq %d step %d: rejected op changed state:\nbefore %+v\nafter  %+v",
						seq, step, before, after)
				}
			}
			q := b.QuotaState()
			if q.Used < 0 || q.Used > cfg.Q {
				t.Fatalf("seq %d step %d: used %d out of [0,%d]", seq, step, q.Used, cfg.Q)
			}
			if b.MaxNow() != nv.maxNow {
				t.Fatalf("seq %d step %d: maxNow got %d want %d",
					seq, step, b.MaxNow(), nv.maxNow)
			}
		}

		for _, k := range b.Keys() {
			bo, _ := b.Lookup(k)
			no, ok := nv.objs[k]
			if !ok || bo.Size != no[0] || bo.LA != no[1] {
				t.Fatalf("seq %d: final object %s mismatch: got size=%d la=%d naive=%v",
					seq, k, bo.Size, bo.LA, no)
			}
		}
		if len(b.Keys()) != len(nv.objs) {
			t.Fatalf("seq %d: object count mismatch", seq)
		}
		q := b.QuotaState()
		if q.Period != nv.period || q.Used != nv.used {
			t.Fatalf("seq %d: final quota mismatch got p=%d u=%d naive p=%d u=%d",
				seq, q.Period, q.Used, nv.period, nv.used)
		}
	}
}

// TestConcurrent：并发调用不应产生数据竞争或状态破坏（配合 -race）。
func TestConcurrent(t *testing.T) {
	b, _ := New(exampleCfg())
	const workers = 16
	var wg sync.WaitGroup
	var maxSeen atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id) + 7))
			key := string(rune('a' + id))
			now := int64(0)
			for i := 0; i < 200; i++ {
				now += int64(r.Intn(3))
				switch i % 3 {
				case 0:
					b.Put(key, int64(1+r.Intn(10)), now)
				case 1:
					b.Get(key, now)
				case 2:
					b.Delete(key, now)
				}
				if q := b.QuotaState(); q.Used < 0 || q.Used > exampleCfg().Q {
					t.Errorf("quota invariant broken: %+v", q)
					return
				}
				maxSeen.Store(b.MaxNow())
			}
		}(w)
	}
	wg.Wait()
}
