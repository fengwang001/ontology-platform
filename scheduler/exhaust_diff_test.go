package scheduler_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"ontology/scheduler"
)

// 偏向性差分：大量“恰好领完再确认”的序列，覆盖 pending 归零与完成边界。
func TestRandomExhaustDifferential(t *testing.T) {
	logPath := os.Getenv("SCHED_LOG2")
	var bw *bufio.Writer
	if logPath != "" {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		bw = bufio.NewWriter(f)
		defer bw.Flush()
	}

	const N = 2000
	for seed := int64(10000); seed < 10000+N; seed++ {
		r := rand.New(rand.NewSource(seed))
		s := scheduler.New()
		m := newNaive()
		if bw != nil {
			fmt.Fprintf(bw, "==== seed=%d ====\n", seed)
		}

		// 1~3 个小余量区间，Process 的 n 经常恰好取完。
		nIV := 1 + r.Intn(3)
		for i := 0; i < nIV; i++ {
			size := int64(1 + r.Intn(6))
			base := int64(r.Intn(50))
			s.Add(base, base+size)
			m.add(base, base+size)
		}
		for step := 0; step < 20; step++ {
			id := int64(1 + r.Intn(nIV))
			if r.Intn(2) == 0 {
				n := int64(1 + r.Intn(6))
				k, lo, hi, b, ge := s.Process(id, n)
				mk, mlo, mhi, mb, me := m.process(id, n)
				if bw != nil {
					fmt.Fprintf(bw, "Process(%d,%d) -> k=%d [%d,%d) b=%d %s W=%d/%d\n",
						id, n, k, lo, hi, b, errKey(ge), s.Watermark(), m.w)
				}
				if (ge == nil) != (me == "") || k != mk || lo != mlo || hi != mhi || b != mb {
					t.Fatalf("seed=%d step=%d Process(%d,%d) got %d,%d,%d,%d,%v want %d,%d,%d,%d,%q",
						seed, step, id, n, k, lo, hi, b, ge, mk, mlo, mhi, mb, me)
				}
			} else {
				num := int64(r.Intn(3))
				den := int64(1 + r.Intn(4))
				if num > den {
					num = den
				}
				nid, ge := s.Split(id, num, den)
				mnid, me := m.split(id, num, den)
				if bw != nil {
					fmt.Fprintf(bw, "Split(%d,%d,%d) -> new=%d %s W=%d/%d\n",
						id, num, den, nid, errKey(ge), s.Watermark(), m.w)
				}
				if errKey(ge) != "" && ge != errMap[me] {
					t.Fatalf("seed=%d Split err got %v want %q", seed, ge, me)
				}
				if me == "" && nid != mnid {
					t.Fatalf("seed=%d Split id got %d want %d", seed, nid, mnid)
				}
				if me == "" {
					nIV++
				}
			}
			// 任意确认存在的批（含乱序/重复）。
			if m.batchCnt > 0 {
				bid := int64(1 + r.Intn(int(m.batchCnt)))
				ge := s.Ack(bid)
				me := m.ack(bid)
				if bw != nil {
					fmt.Fprintf(bw, "Ack(%d) -> %s W=%d/%d\n", bid, errKey(ge), s.Watermark(), m.w)
				}
				if me == "" && ge != nil {
					t.Fatalf("seed=%d Ack(%d) unexpected %v", seed, bid, ge)
				}
				if me != "" && ge != errMap[me] {
					t.Fatalf("seed=%d Ack(%d) got %v want %q", seed, bid, ge, me)
				}
			}
			if s.Watermark() != m.w {
				t.Fatalf("seed=%d step=%d W got %d want %d", seed, step, s.Watermark(), m.w)
			}
			if bw != nil {
				_ = bw.WriteByte('\n')
			}
		}
		for id := int64(1); id <= m.idCount; id++ {
			if m.intervals[id] == nil {
				continue
			}
			p, e := s.Progress(id)
			if e != nil {
				t.Fatalf("Progress(%d): %v", id, e)
			}
			miv := m.intervals[id]
			pending := 0
			for _, b := range miv.batches {
				if !b.acked {
					pending++
				}
			}
			if p.From != miv.from || p.To != miv.to || p.Next != miv.next ||
				p.Remaining != miv.to-miv.next || p.Pending != pending {
				t.Fatalf("seed=%d iv=%d %+v vs %+v pending=%d", seed, id, p, *miv, pending)
			}
		}
	}
}
