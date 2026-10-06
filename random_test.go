package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type diffKind int

const (
	dkSetLimit diffKind = iota
	dkRegisterPrice
	dkReceive
	dkReturn
	dkConsume
	dkReverse
	dkStatement
	dkOnHand
	dkReversedQty
)

type diffOp struct {
	kind                     diffKind
	t                        int64
	supplier, product        ID
	number                   BatchID
	quantity, duration       int64
	start, end, price, limit int64
	line                     LineID
}

func (o diffOp) String() string {
	switch o.kind {
	case dkSetLimit:
		return fmt.Sprintf("SetLimit(t=%d s=%d p=%d limit=%d)", o.t, o.supplier, o.product, o.limit)
	case dkRegisterPrice:
		return fmt.Sprintf("RegisterPrice(t=%d s=%d p=%d [%d,%d) price=%d)",
			o.t, o.supplier, o.product, o.start, o.end, o.price)
	case dkReceive:
		return fmt.Sprintf("Receive(t=%d s=%d p=%d qty=%d dur=%d)",
			o.t, o.supplier, o.product, o.quantity, o.duration)
	case dkReturn:
		return fmt.Sprintf("Return(t=%d s=%d p=%d batch=%d qty=%d)",
			o.t, o.supplier, o.product, o.number, o.quantity)
	case dkConsume:
		return fmt.Sprintf("Consume(t=%d p=%d qty=%d)", o.t, o.product, o.quantity)
	case dkReverse:
		return fmt.Sprintf("Reverse(t=%d line=%d qty=%d)", o.t, o.line, o.quantity)
	case dkStatement:
		return fmt.Sprintf("Statement(s=%d [%d,%d))", o.supplier, o.start, o.end)
	case dkOnHand:
		return fmt.Sprintf("OnHand(s=%d p=%d)", o.supplier, o.product)
	case dkReversedQty:
		return fmt.Sprintf("ReversedQty(line=%d)", o.line)
	}
	return "?"
}

type diffWorld struct {
	real    *System
	naive   *naiveSystem
	rng     *rand.Rand
	clock   int64
	lines   []LineID
	batches map[[2]int64][]BatchID
	log     strings.Builder
}

func newDiffWorld(seed int64) *diffWorld {
	return &diffWorld{
		real:    NewSystem(),
		naive:   newNaiveSystem(),
		rng:     rand.New(rand.NewSource(seed)),
		batches: map[[2]int64][]BatchID{},
	}
}

func (w *diffWorld) advance() int64 {
	if w.rng.Intn(3) != 0 {
		w.clock++
	}
	return w.clock
}

func (w *diffWorld) record(format string, args ...any) {
	fmt.Fprintf(&w.log, format+"\n", args...)
}

func errKind(err error) ErrorKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return ""
}

func key2(s, p ID) [2]int64 { return [2]int64{int64(s), int64(p)} }

func (w *diffWorld) compareErr(t *testing.T, o diffOp, e1, e2 error) {
	t.Helper()
	k1, k2 := errKind(e1), errKind(e2)
	if k1 != k2 {
		t.Fatalf("error kind mismatch on %s: real=%s naive=%s\n%s",
			o, k1, k2, w.log.String())
	}
	if k1 == "" {
		w.record("  -> accepted")
	} else {
		w.record("  -> rejected kind=%s (both agree)", k1)
	}
}

func (w *diffWorld) apply(t *testing.T, o diffOp) {
	t.Helper()
	w.record("OP %s", o)
	switch o.kind {
	case dkSetLimit:
		e1 := w.real.SetLimit(o.t, o.supplier, o.product, o.limit)
		e2 := w.naive.setLimit(o.t, o.supplier, o.product, o.limit)
		w.compareErr(t, o, e1, e2)
	case dkRegisterPrice:
		e1 := w.real.RegisterPrice(o.t, o.supplier, o.product, o.start, o.end, o.price)
		e2 := w.naive.registerPrice(o.t, o.supplier, o.product, o.start, o.end, o.price)
		w.compareErr(t, o, e1, e2)
	case dkReceive:
		n1, e1 := w.real.Receive(o.t, o.supplier, o.product, o.quantity, o.duration)
		n2, e2 := w.naive.receive(o.t, o.supplier, o.product, o.quantity, o.duration)
		w.compareErr(t, o, e1, e2)
		if e1 == nil && n1 != n2 {
			t.Fatalf("batch number differs on %s: real=%d naive=%d\n%s",
				o, n1, n2, w.log.String())
		}
		if e1 == nil {
			k := key2(o.supplier, o.product)
			w.batches[k] = append(w.batches[k], n1)
		}
	case dkReturn:
		e1 := w.real.Return(o.t, o.supplier, o.product, o.number, o.quantity)
		e2 := w.naive.returnBatch(o.t, o.supplier, o.product, o.number, o.quantity)
		w.compareErr(t, o, e1, e2)
	case dkConsume:
		r1, e1 := w.real.Consume(o.t, o.product, o.quantity)
		r2, e2 := w.naive.consume(o.t, o.product, o.quantity)
		w.compareErr(t, o, e1, e2)
		if e1 == nil {
			if r2 == nil || len(r1.Lines) != len(r2.Lines) {
				t.Fatalf("receipt length mismatch on %s: real=%+v naive=%+v\n%s",
					o, r1, r2, w.log.String())
			}
			for i := range r1.Lines {
				if r1.Lines[i] != r2.Lines[i] {
					t.Fatalf("line %d mismatch on %s: real=%+v naive=%+v\n%s",
						i, o, r1.Lines[i], r2.Lines[i], w.log.String())
				}
			}
			for _, l := range r1.Lines {
				w.lines = append(w.lines, l.ID)
			}
		}
	case dkReverse:
		e1 := w.real.Reverse(o.t, o.line, o.quantity)
		e2 := w.naive.reverse(o.t, o.line, o.quantity)
		w.compareErr(t, o, e1, e2)
	case dkStatement:
		s1, e1 := w.real.Statement(o.supplier, o.start, o.end)
		s2, e2 := w.naive.statement(o.supplier, o.start, o.end)
		w.compareErr(t, o, e1, e2)
		if e1 == nil && *s1 != *s2 {
			t.Fatalf("statement mismatch on %s: real=%+v naive=%+v\n%s",
				o, *s1, *s2, w.log.String())
		}
	case dkOnHand:
		t1, x1 := w.real.OnHand(o.supplier, o.product)
		t2, x2 := w.naive.onHandQuery(o.supplier, o.product)
		if t1 != t2 || x1 != x2 {
			t.Fatalf("onhand mismatch on %s: real=(%d,%d) naive=(%d,%d)\n%s",
				o, t1, x1, t2, x2, w.log.String())
		}
		w.record("  -> OnHand total=%d expired=%d (both agree)", t1, x1)
	case dkReversedQty:
		q1, e1 := w.real.ReversedQty(o.line)
		q2, e2 := w.naive.reversedQty(o.line)
		w.compareErr(t, o, e1, e2)
		if e1 == nil && q1 != q2 {
			t.Fatalf("reversed qty mismatch on %s: real=%d naive=%d\n%s",
				o, q1, q2, w.log.String())
		}
	}
}

func (w *diffWorld) snapshot(t *testing.T) {
	t.Helper()
	for k, nums := range w.batches {
		s, p := ID(k[0]), ID(k[1])
		t1, x1 := w.real.OnHand(s, p)
		t2, x2 := w.naive.onHandQuery(s, p)
		if t1 != t2 || x1 != x2 {
			t.Fatalf("final onhand mismatch s=%d p=%d: real=(%d,%d) naive=(%d,%d)",
				s, p, t1, x1, t2, x2)
		}
		// 每个批次的剩余量与 0<=rem<=qty 不变量在真实系统中直接核对。
		for _, n := range nums {
			b := w.real.batches[productKey{s, p}][n]
			if b.Remaining < 0 || b.Remaining > b.Quantity {
				t.Fatalf("batch invariant violated s=%d p=%d b=%d rem=%d qty=%d",
					s, p, n, b.Remaining, b.Quantity)
			}
			nb := w.naive.batches[pk{s, p}][n]
			if b.Remaining != nb.remaining {
				t.Fatalf("batch remaining mismatch s=%d p=%d b=%d real=%d naive=%d",
					s, p, n, b.Remaining, nb.remaining)
			}
		}
	}
	for _, id := range w.lines {
		q1, _ := w.real.ReversedQty(id)
		q2, _ := w.naive.reversedQty(id)
		if q1 != q2 {
			t.Fatalf("final reversed mismatch line=%d real=%d naive=%d", id, q1, q2)
		}
	}
}

// checkBatches 核对两套模型共有批次的剩余量是否一致。
func (w *diffWorld) checkBatches(t *testing.T, o diffOp) {
	t.Helper()
	for k, m := range w.real.batches {
		nm := w.naive.batches[pk{k.supplier, k.product}]
		for n, b := range m {
			if nb, ok := nm[n]; ok && b.Remaining != nb.remaining {
				t.Fatalf("batch divergence at %s: s=%d p=%d b=%d real rem=%d(arr=%d,dur=%d) naive rem=%d(arr=%d,dur=%d) clock=%d\n%s",
					o, k.supplier, k.product, n,
					b.Remaining, b.Arrival, b.Duration,
					nb.remaining, nb.arrival, nb.duration,
					w.real.clock, w.log.String())
			}
		}
	}
}

func (w *diffWorld) checkHeap(t *testing.T, o diffOp, now int64) {
	t.Helper()
	for k, m := range w.real.batches {
		h := w.real.index.heaps[k.product]
		present := map[*batchEntry]bool{}
		if h != nil {
			for _, e := range *h {
				present[e] = true
			}
		}
		for n, b := range m {
			available := b.Remaining > 0 && !b.ExpiredAtTime(now)
			if !available {
				continue
			}
			em := w.real.index.entries[k]
			e := em[n]
			if e == nil || !e.inHeap || !present[e] {
				hist := w.log.String()
				if len(hist) > 2500 {
					hist = hist[len(hist)-2500:]
				}
				t.Fatalf("heap invariant broken at %s (now=%d): s=%d p=%d b=%d rem=%d arr=%d expAt=%d inHeap=%v present=%v\nhistory:\n%s",
					o, now, k.supplier, k.product, n, b.Remaining, b.Arrival,
					b.ExpiredAt(), e != nil && e.inHeap, present[e], hist)
			}
		}
	}
}

func (w *diffWorld) genOp() diffOp {
	r := w.rng
	o := diffOp{}
	roll := r.Intn(100)
	switch {
	case roll < 12:
		o.kind = dkSetLimit
		o.supplier = ID(1 + r.Intn(3))
		o.product = ID(1 + r.Intn(3))
		o.limit = int64(1 + r.Intn(12))
		o.t = w.advance()
	case roll < 24:
		o.kind = dkRegisterPrice
		o.supplier = ID(1 + r.Intn(3))
		o.product = ID(1 + r.Intn(3))
		o.start = int64(r.Intn(6)) * 5
		o.end = o.start + int64(1+r.Intn(4))*5
		o.price = int64(1 + r.Intn(20))
		o.t = w.advance()
	case roll < 45:
		o.kind = dkReceive
		o.supplier = ID(1 + r.Intn(3))
		o.product = ID(1 + r.Intn(3))
		o.quantity = int64(1 + r.Intn(6))
		o.duration = []int64{0, 1, 2, 3, 10}[r.Intn(5)]
		o.t = w.advance()
	case roll < 58:
		o.kind = dkReturn
		o.supplier = ID(1 + r.Intn(3))
		o.product = ID(1 + r.Intn(3))
		k := key2(o.supplier, o.product)
		if bs := w.batches[k]; len(bs) > 0 {
			o.number = bs[r.Intn(len(bs))]
		} else {
			o.number = BatchID(1 + r.Intn(3))
		}
		o.quantity = int64(1 + r.Intn(5))
		o.t = w.advance()
	case roll < 80:
		o.kind = dkConsume
		o.product = ID(1 + r.Intn(3))
		o.quantity = int64(1 + r.Intn(7))
		o.t = w.advance()
	case roll < 90:
		o.kind = dkReverse
		if len(w.lines) > 0 {
			o.line = w.lines[r.Intn(len(w.lines))]
		} else {
			o.line = LineID(1 + r.Intn(5))
		}
		o.quantity = int64(1 + r.Intn(4))
		o.t = w.advance()
	case roll < 96:
		o.kind = dkStatement
		o.supplier = ID(1 + r.Intn(3))
		o.start = int64(r.Intn(int(w.clock) + 2))
		o.end = o.start + int64(r.Intn(4))
	default:
		o.kind = dkOnHand
		o.supplier = ID(1 + r.Intn(3))
		o.product = ID(1 + r.Intn(3))
	}
	// 注入时钟回退：回退操作不携带数量语义修改。
	if o.t > 0 && r.Intn(15) == 0 {
		o.t = w.clock - int64(1+r.Intn(3))
	}
	// 注入非法数量：参数非法应优先于时钟回退。
	if r.Intn(40) == 0 {
		o.quantity = -int64(1 + r.Intn(3))
	}
	return o
}

// TestRandomDifferential 与朴素模型对照大量随机操作序列。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential test in -short mode")
	}
	const runs, ops = 80, 1200
	for seed := int64(0); seed < runs; seed++ {
		w := newDiffWorld(seed)
		for i := 0; i < ops; i++ {
			o := w.genOp()
			w.apply(t, o)
			w.checkBatches(t, o)
			w.checkHeap(t, o, w.real.clock)
		}
		w.snapshot(t)
	}
}

// TestRandomDifferentialLog 小规模跑一个固定种子并打印
// “输入、输出与判定依据”日志，作为可直接查阅的判定轨迹。
func TestRandomDifferentialLog(t *testing.T) {
	w := newDiffWorld(42)
	for i := 0; i < 40; i++ {
		w.apply(t, w.genOp())
	}
	w.snapshot(t)
	t.Logf("\n%s", w.log.String())
}
