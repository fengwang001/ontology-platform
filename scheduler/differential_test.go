package scheduler_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"ontology/scheduler"
)

// 操作编码：便于序列化重放与打印。
type rop struct {
	kind    int // 0 Add 1 Process 2 Split 3 Ack
	a, b, c int64
}

func (o rop) String() string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("Add(%d,%d)", o.a, o.b)
	case 1:
		return fmt.Sprintf("Process(%d,%d)", o.a, o.b)
	case 2:
		return fmt.Sprintf("Split(%d,%d,%d)", o.a, o.b, o.c)
	default:
		return fmt.Sprintf("Ack(%d)", o.a)
	}
}

func genSeq(r *rand.Rand) []rop {
	const L = 24
	ops := make([]rop, 0, L)
	var maxID, maxBatch int64
	for len(ops) < L {
		var o rop
		switch x := r.Intn(10); {
		case x <= 2: // Add，含非法/水位线附近
			var from, to int64
			if r.Intn(3) == 0 {
				from = int64(r.Intn(40)) // 刻意打水位线附近
			} else {
				from = int64(r.Intn(200))
			}
			to = from + 1 + int64(r.Intn(12))
			if r.Intn(12) == 0 {
				to = from // 非法
			}
			o = rop{0, from, to, 0}
		case x >= 3 && x <= 5: // Process
			id := 1 + int64(r.Intn(int(maxID)+3))
			n := 1 + int64(r.Intn(8))
			o = rop{1, id, n, 0}
		case x == 6 || x == 7: // Split
			id := 1 + int64(r.Intn(int(maxID)+3))
			den := 1 + int64(r.Intn(6))
			num := int64(r.Intn(int(den) + 1))
			if r.Intn(4) == 0 {
				num = 0
			}
			o = rop{2, id, num, den}
		default: // Ack，含乱序与重复
			if maxBatch == 0 {
				continue
			}
			bid := int64(1 + r.Intn(int(maxBatch)+2))
			o = rop{3, bid, 0, 0}
		}
		ops = append(ops, o)
		// 预估编号增长（Add/Split 成功各 +1），仅用于抽样范围，无需精确。
		last := o
		if last.kind == 0 || last.kind == 2 {
			maxID++
		}
		if last.kind == 1 {
			maxBatch++
		}
	}
	return ops
}

var errMap = map[string]error{
	"invalid":      scheduler.ErrInvalid,
	"below":        scheduler.ErrBelowWatermark,
	"nointerval":   scheduler.ErrNoInterval,
	"exhausted":    scheduler.ErrExhausted,
	"cannotsplit":  scheduler.ErrCannotSplit,
	"nobatch":      scheduler.ErrNoBatch,
	"alreadyacked": scheduler.ErrAlreadyAcked,
}

func TestRandomDifferential(t *testing.T) {
	logPath := os.Getenv("SCHED_LOG")
	var lf *os.File
	var bw *bufio.Writer
	if logPath != "" {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		lf = f
		bw = bufio.NewWriter(f)
		defer func() {
			bw.Flush()
			lf.Close()
		}()
	}

	const N = 2000
	for seed := int64(1); seed <= N; seed++ {
		r := rand.New(rand.NewSource(seed))
		seq := genSeq(r)
		s := scheduler.New()
		m := newNaive()

		if bw != nil {
			fmt.Fprintf(bw, "==== seed=%d ops=%d ====\n", seed, len(seq))
		}

		for step, o := range seq {
			var gotN []int64
			var gotE error
			var probeDelta int64

			p0 := s.HoldProbes()
			switch o.kind {
			case 0:
				id, e := s.Add(o.a, o.b)
				gotN, gotE = []int64{id}, e
			case 1:
				k, lo, hi, b, e := s.Process(o.a, o.b)
				gotN, gotE = []int64{k, lo, hi, b}, e
			case 2:
				nid, e := s.Split(o.a, o.b, o.c)
				gotN, gotE = []int64{nid}, e
			case 3:
				gotE = s.Ack(o.a)
			}
			probeDelta = s.HoldProbes() - p0

			var wantN []int64
			var wantE string
			switch o.kind {
			case 0:
				id, e := m.add(o.a, o.b)
				wantN, wantE = []int64{id}, e
			case 1:
				k, lo, hi, b, e := m.process(o.a, o.b)
				wantN, wantE = []int64{k, lo, hi, b}, e
			case 2:
				nid, e := m.split(o.a, o.b, o.c)
				wantN, wantE = []int64{nid}, e
			case 3:
				wantE = m.ack(o.a)
			}

			mismatch := false
			if gotE != nil {
				exp := errMap[wantE]
				if !(wantE != "" && gotE == exp) {
					mismatch = true
				}
			} else if wantE != "" {
				mismatch = true
			}
			if wantE == "" && gotE == nil && fmt.Sprint(gotN) != fmt.Sprint(wantN) {
				mismatch = true
			}
			if s.Watermark() != m.w {
				mismatch = true
			}
			// 探测预算：每操作 <= 4 + 本次丢弃的失效项数。
			// 序列中一次操作最多完成一个区间，故全局失效链至多 1 项；
			// delta 上界 3（区间堆查看 + 全局失效项 + 新堆顶）。
			if probeDelta > 4 {
				t.Fatalf("seed=%d step=%d %s probes=%d > 4", seed, step, o, probeDelta)
			}

			if bw != nil {
				reason := "accepted"
				if gotE != nil {
					reason = "reject=" + shortErr(gotE)
				}
				fmt.Fprintf(bw, "[%02d] %-22s -> n=%v %s W=%d naiveW=%d probes+=:%d : %s\n",
					step, o.String(), gotN, mark(gotE), s.Watermark(), m.w, probeDelta, reason)
			}

			if mismatch {
				t.Fatalf("seed=%d step=%d op=%s got=%v(%v) W=%d want=%v(%q) W=%d",
					seed, step, o, gotN, gotE, s.Watermark(), wantN, wantE, m.w)
			}

			// 每步对照全部区间快照。
			for id := int64(1); id <= m.idCount; id++ {
				miv := m.intervals[id]
				if miv == nil {
					continue
				}
				p, e := s.Progress(id)
				if e != nil {
					t.Fatalf("seed=%d Progress(%d): %v", seed, id, e)
				}
				pending := 0
				for _, b := range miv.batches {
					if !b.acked {
						pending++
					}
				}
				if p.From != miv.from || p.To != miv.to || p.Next != miv.next ||
					p.Remaining != miv.to-miv.next || p.Pending != pending {
					t.Fatalf("seed=%d iv=%d got=%+v want=[%d,%d) next=%d pend=%d",
						seed, id, p, miv.from, miv.to, miv.next, pending)
				}
			}
		}
	}
}

func mark(e error) string {
	if e == nil {
		return ""
	}
	return "err=" + shortErr(e)
}

func shortErr(e error) string {
	s := e.Error()
	s = strings.TrimPrefix(s, "scheduler: ")
	return s
}
