package booking

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveState 是按规格逐子集枚举的朴素参考实现（单线程）。
type naiveState struct {
	c        int
	rho      int64
	stocks   []int64
	capacity []int64
	booked   map[string]naiveContract
	waiting  []naiveContract
	log      []string
}

type naiveContract struct {
	id   string
	mask uint32
	qty  int64
}

type naiveResult struct {
	ok       bool
	reason   error
	phase    Phase
	promoted []string
}

func newNaive(c int, stocks []int64, rho int) *naiveState {
	cap := make([]int64, 1<<uint(c))
	for t := 1; t < len(cap); t++ {
		var sum int64
		for j := 0; j < c; j++ {
			if uint32(t)&(1<<uint(j)) != 0 {
				sum += stocks[j]
			}
		}
		cap[t] = int64(rho) * sum / 100
	}
	return &naiveState{
		c: c, rho: int64(rho), stocks: stocks, capacity: cap,
		booked: map[string]naiveContract{},
	}
}

func (n *naiveState) inWaiting(id string) int {
	for i, w := range n.waiting {
		if w.id == id {
			return i
		}
	}
	return -1
}

// feasibleAdd 朴素枚举全部 2^C-1 个非空子集，判断加入合约后是否可行。
func (n *naiveState) feasibleAdd(mask uint32, qty int64) bool {
	for t := 1; t < len(n.capacity); t++ {
		var load int64
		for _, b := range n.booked {
			if b.mask&uint32(t) == b.mask {
				load += b.qty
			}
		}
		if mask&uint32(t) == mask {
			load += qty
		}
		if load > n.capacity[t] {
			return false
		}
	}
	return true
}

// feasibleReplace 判断把 id 的 (mask,qty) 替换为新值后，全量子集是否可行。
func (n *naiveState) feasibleReplace(id string, mask2 uint32, q2 int64) bool {
	for t := 1; t < len(n.capacity); t++ {
		var load int64
		for bid, b := range n.booked {
			if bid == id {
				continue
			}
			if b.mask&uint32(t) == b.mask {
				load += b.qty
			}
		}
		if mask2&uint32(t) == mask2 {
			load += q2
		}
		if load > n.capacity[t] {
			return false
		}
	}
	return true
}

// promote 一轮递补：按到达顺序只扫一遍，不阻塞队首。
func (n *naiveState) promote() []string {
	promoted := []string{}
	kept := n.waiting[:0]
	for _, w := range n.waiting {
		if n.feasibleAdd(w.mask, w.qty) {
			n.booked[w.id] = w
			promoted = append(promoted, w.id)
			n.log = append(n.log, fmt.Sprintf("    PROMOTE %s mask=%0*b qty=%d",
				w.id, n.c, w.mask, w.qty))
		} else {
			kept = append(kept, w)
		}
	}
	n.waiting = kept
	return promoted
}

func (n *naiveState) validMask(m uint32) bool { return m != 0 && m < uint32(1<<uint(n.c)) }
func (n *naiveState) validQty(q int64) bool   { return q >= 1 && q <= 1e12 }

func reasonStr(r error) string {
	if r == nil {
		return "ok"
	}
	return r.Error()
}

func (n *naiveState) book(id string, mask uint32, qty int64) naiveResult {
	n.log = append(n.log, fmt.Sprintf("  Book(%s,%0*b,%d)", id, n.c, mask, qty))
	if id == "" || !n.validMask(mask) || !n.validQty(qty) {
		return naiveResult{reason: ErrInvalid}
	}
	if _, ok := n.booked[id]; ok {
		return naiveResult{reason: ErrAlreadyExists, phase: PhaseBooked}
	}
	if n.inWaiting(id) >= 0 {
		return naiveResult{reason: ErrAlreadyExists, phase: PhaseWaiting}
	}
	if qty > n.capacity[mask] {
		n.log = append(n.log, "    -> NEVER_BOOKABLE")
		return naiveResult{reason: ErrNeverBookable}
	}
	if n.feasibleAdd(mask, qty) {
		n.booked[id] = naiveContract{id, mask, qty}
		n.log = append(n.log, "    -> BOOKED")
		return naiveResult{ok: true, phase: PhaseBooked}
	}
	n.waiting = append(n.waiting, naiveContract{id, mask, qty})
	n.log = append(n.log, "    -> WAITING")
	return naiveResult{ok: false, reason: ErrInfeasible, phase: PhaseWaiting}
}

func (n *naiveState) cancel(id string) naiveResult {
	n.log = append(n.log, fmt.Sprintf("  Cancel(%s)", id))
	if id == "" {
		return naiveResult{reason: ErrInvalid}
	}
	if _, ok := n.booked[id]; ok {
		delete(n.booked, id)
		p := n.promote()
		return naiveResult{ok: true, promoted: p}
	}
	if i := n.inWaiting(id); i >= 0 {
		n.waiting = append(n.waiting[:i], n.waiting[i+1:]...)
		n.log = append(n.log, "    -> removed from waiting (no promotion)")
		return naiveResult{ok: true, phase: PhaseWaiting}
	}
	return naiveResult{reason: ErrNotFound}
}

func (n *naiveState) resize(id string, q2 int64) naiveResult {
	n.log = append(n.log, fmt.Sprintf("  Resize(%s,%d)", id, q2))
	if id == "" || !n.validQty(q2) {
		return naiveResult{reason: ErrInvalid}
	}
	b, ok := n.booked[id]
	if !ok {
		if n.inWaiting(id) >= 0 {
			return naiveResult{reason: ErrNotBooked, phase: PhaseWaiting}
		}
		return naiveResult{reason: ErrNotFound}
	}
	if q2 == b.qty {
		return naiveResult{ok: true, phase: PhaseBooked}
	}
	if q2 < b.qty {
		b.qty = q2
		n.booked[id] = b
		p := n.promote()
		return naiveResult{ok: true, phase: PhaseBooked, promoted: p}
	}
	if !n.feasibleReplace(id, b.mask, q2) {
		n.log = append(n.log, "    -> INFEASIBLE rejected")
		return naiveResult{ok: false, reason: ErrInfeasible, phase: PhaseBooked}
	}
	b.qty = q2
	n.booked[id] = b
	n.log = append(n.log, "    -> resized larger")
	return naiveResult{ok: true, phase: PhaseBooked}
}

func (n *naiveState) retarget(id string, mask2 uint32) naiveResult {
	n.log = append(n.log, fmt.Sprintf("  Retarget(%s,%0*b)", id, n.c, mask2))
	if id == "" || !n.validMask(mask2) {
		return naiveResult{reason: ErrInvalid}
	}
	b, ok := n.booked[id]
	if !ok {
		if n.inWaiting(id) >= 0 {
			return naiveResult{reason: ErrNotBooked, phase: PhaseWaiting}
		}
		return naiveResult{reason: ErrNotFound}
	}
	if mask2 == b.mask {
		return naiveResult{ok: true, phase: PhaseBooked}
	}
	if !n.feasibleReplace(id, mask2, b.qty) {
		n.log = append(n.log, "    -> INFEASIBLE rejected")
		return naiveResult{ok: false, reason: ErrInfeasible, phase: PhaseBooked}
	}
	b.mask = mask2
	n.booked[id] = b
	p := n.promote()
	return naiveResult{ok: true, phase: PhaseBooked, promoted: p}
}

func (n *naiveState) snapshot() ([]naiveContract, []naiveContract) {
	booked := make([]naiveContract, 0, len(n.booked))
	for _, b := range n.booked {
		booked = append(booked, b)
	}
	sort.Slice(booked, func(i, j int) bool { return booked[i].id < booked[j].id })
	return booked, append([]naiveContract(nil), n.waiting...)
}

type op struct {
	kind int // 0 book 1 cancel 2 resize 3 retarget
	id   string
	mask uint32
	qty  int64
}

func genSequence(rng *rand.Rand, c int, seqLen int) ([]op, []int64, int) {
	stocks := make([]int64, c)
	for i := range stocks {
		stocks[i] = rng.Int63n(200) // 0..199
	}
	rho := []int{50, 80, 90, 100, 120, 200}[rng.Intn(6)]
	ops := make([]op, seqLen)
	known := []string{}
	for k := range ops {
		if len(known) > 0 && rng.Intn(100) < 45 {
			id := known[rng.Intn(len(known))]
			switch rng.Intn(3) {
			case 0:
				ops[k] = op{kind: 1, id: id}
			case 1:
				ops[k] = op{kind: 2, id: id, qty: 1 + rng.Int63n(260)}
			default:
				m := uint32(1 << uint(rng.Intn(c)))
				if rng.Intn(2) == 0 {
					m |= uint32(1 << uint(rng.Intn(c)))
				}
				ops[k] = op{kind: 3, id: id, mask: m}
			}
			continue
		}
		id := fmt.Sprintf("c%03d", k)
		known = append(known, id)
		m := uint32(1 << uint(rng.Intn(c)))
		for j := 1; j < c; j++ {
			if rng.Intn(2) == 1 {
				m |= uint32(1 << uint(j))
			}
		}
		qty := int64(1)
		switch rng.Intn(12) {
		case 0:
			qty = 0 // 故意非法
		case 1:
			qty = 1_000_000_000_001 // 故意非法
		default:
			qty = 1 + rng.Int63n(220)
		}
		ops[k] = op{kind: 0, id: id, mask: m, qty: qty}
	}
	return ops, stocks, rho
}

func sameReason(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Error() == b.Error()
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statesEqual(real *Book, n *naiveState) bool {
	rb := real.BookedContracts()
	rw := real.WaitingContracts()
	nb, nw := n.snapshot()
	if len(rb) != len(nb) || len(rw) != len(nw) {
		return false
	}
	for i := range rb {
		if rb[i].ID != nb[i].id || rb[i].Mask != nb[i].mask || rb[i].Qty != nb[i].qty {
			return false
		}
	}
	for i := range rw {
		if rw[i].ID != nw[i].id || rw[i].Mask != nw[i].mask || rw[i].Qty != nw[i].qty {
			return false
		}
	}
	return true
}

func dumpNaive(n *naiveState) string {
	b, w := n.snapshot()
	return fmt.Sprintf("booked=%v waiting=%v", b, w)
}

// 2000 组随机操作序列：实现与朴素模拟逐步对照（结果、拒绝原因、阶段、递补、终态）。
func TestNaiveDifferential2000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential test in short mode")
	}
	const sequences = 2000
	const seqLen = 60
	failLogs := []string{}
	var executed int
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s) + 1))
		c := 1 + rng.Intn(5) // C 1..5
		ops, stocks, rho := genSequence(rng, c, seqLen)

		real := mustNew(t, c, stocks, rho)
		naive := newNaive(c, stocks, rho)
		naive.log = append(naive.log,
			fmt.Sprintf("seed=%d c=%d stocks=%v rho=%d", s+1, c, stocks, rho))

		var failMsg string
		for step, o := range ops {
			var rr Result
			var nr naiveResult
			switch o.kind {
			case 0:
				rr = real.Book(o.id, o.mask, o.qty)
				nr = naive.book(o.id, o.mask, o.qty)
			case 1:
				rr = real.Cancel(o.id)
				nr = naive.cancel(o.id)
			case 2:
				rr = real.Resize(o.id, o.qty)
				nr = naive.resize(o.id, o.qty)
			case 3:
				rr = real.Retarget(o.id, o.mask)
				nr = naive.retarget(o.id, o.mask)
			}
			naive.log = append(naive.log, fmt.Sprintf(
				"    => real ok=%v reason=%s phase=%d promoted=%v | naive ok=%v reason=%s phase=%d promoted=%v",
				rr.OK, reasonStr(rr.Reason), rr.Phase, rr.Promoted,
				nr.ok, reasonStr(nr.reason), nr.phase, nr.promoted))

			mismatch := rr.OK != nr.ok ||
				!sameReason(rr.Reason, nr.reason) ||
				rr.Phase != nr.phase ||
				!sameStringSlice(rr.Promoted, nr.promoted)
			if mismatch {
				failMsg = fmt.Sprintf("seed=%d step=%d op=%+v\nreal=%+v\nnaive=%+v",
					s+1, step, o, rr, nr)
				break
			}
			if !statesEqual(real, naive) {
				failMsg = fmt.Sprintf("seed=%d step=%d state mismatch\nreal booked=%v waiting=%v\nnaive %s",
					s+1, step, real.BookedContracts(), real.WaitingContracts(), dumpNaive(naive))
				break
			}
		}
		executed++
		if failMsg != "" {
			failLogs = append(failLogs, strings.Join(naive.log, "\n")+"\n"+failMsg)
			if len(failLogs) >= 3 {
				break
			}
		}
	}
	if len(failLogs) > 0 {
		t.Fatalf("differential mismatch (%d sequences executed):\n%s",
			executed, strings.Join(failLogs, "\n\n--------------------\n\n"))
	}
	t.Logf("differential test: %d sequences x %d ops all matched", executed, seqLen)
}

// 确定性：同一操作序列在两个独立预订簿上重放，终态与每步结果完全一致。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	ops, stocks, rho := genSequence(rng, 4, 300)
	b1 := mustNew(t, 4, stocks, rho)
	b2 := mustNew(t, 4, stocks, rho)
	for _, o := range ops {
		var r1, r2 Result
		switch o.kind {
		case 0:
			r1, r2 = b1.Book(o.id, o.mask, o.qty), b2.Book(o.id, o.mask, o.qty)
		case 1:
			r1, r2 = b1.Cancel(o.id), b2.Cancel(o.id)
		case 2:
			r1, r2 = b1.Resize(o.id, o.qty), b2.Resize(o.id, o.qty)
		case 3:
			r1, r2 = b1.Retarget(o.id, o.mask), b2.Retarget(o.id, o.mask)
		}
		if fmt.Sprint(r1) != fmt.Sprint(r2) {
			t.Fatalf("nondeterministic result for %+v:\n%+v\n%+v", o, r1, r2)
		}
	}
	if fmt.Sprint(b1.BookedContracts()) != fmt.Sprint(b2.BookedContracts()) ||
		fmt.Sprint(b1.WaitingContracts()) != fmt.Sprint(b2.WaitingContracts()) {
		t.Fatalf("final state differs:\n%v %v\n%v %v",
			b1.BookedContracts(), b1.WaitingContracts(),
			b2.BookedContracts(), b2.WaitingContracts())
	}
}
