package dirtyflush

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

func writeFmt(b *strings.Builder, format string, args ...any) {
	b.WriteString(fmt.Sprintf(format, args...))
}

type genOp struct {
	op     string
	p1, p2 int
	lsn    int64
}

// generateOps 生成一组随机但合法/非法混合的操作；LSN 候选单调推进。
func generateOps(rng *rand.Rand, npages, count int) []genOp {
	var ops []genOp
	var lsn int64
	nextLSN := func() int64 {
		step := int64(1 + rng.Intn(5))
		lsn += step
		return lsn
	}
	for i := 0; i < count; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 34:
			p := rng.Intn(npages)
			l := nextLSN()
			if rng.Intn(8) == 0 {
				l = rng.Int63n(lsn + 1) // 故意制造可能“不够大”的 lsn
				if l == 0 {
					l = 1
				}
			}
			ops = append(ops, genOp{"Modify", p, 0, l})
		case roll < 46:
			l := nextLSN()
			if rng.Intn(8) == 0 {
				l = rng.Int63n(10) // 可能回退
			}
			ops = append(ops, genOp{"SetFlushed", 0, 0, l})
		case roll < 60:
			ops = append(ops, genOp{"FlushStart", rng.Intn(npages), 0, 0})
		case roll < 70:
			ops = append(ops, genOp{"FlushDone", rng.Intn(npages), 0, 0})
		case roll < 86:
			a := rng.Intn(npages)
			b := rng.Intn(npages)
			ops = append(ops, genOp{"AddDep", a, b, 0})
		case roll < 93:
			ops = append(ops, genOp{"Checkpoint", 0, 0, 0})
		default:
			target := int64(1 + rng.Intn(int(lsn)+2))
			ops = append(ops, genOp{"Plan", 0, 0, target})
		}
	}
	return ops
}

func runNaive(D int, ops []genOp) *naive {
	n := newNaive(D)
	for _, o := range ops {
		n.do(o.op, o.p1, o.p2, o.lsn)
	}
	return n
}

func reasonOf(err error) (bool, RejectReason) {
	if err == nil {
		return true, 0
	}
	var oe *OpError
	if asErr := err; asErr != nil {
		if e, ok := asErr.(*OpError); ok {
			oe = e
		}
	}
	if oe == nil {
		return false, -1
	}
	return false, oe.Reason
}

type stateDump struct {
	order   []OrderEntry
	dirtyN  int
	pages   map[int]Page
	out     map[int][]int
	maxMod  int64
	flushed int64
	cp      int64
}

func dumpState(t *testing.T, m *Manager) stateDump {
	d := stateDump{
		order:   m.OrderEntries(),
		dirtyN:  m.DirtyCount(),
		pages:   map[int]Page{},
		out:     map[int][]int{},
		maxMod:  m.MaxModify(),
		flushed: m.Flushed(),
		cp:      m.Checkpoint(),
	}
	for p := range m.touched {
		if pg, ok := m.Snapshot(p); ok {
			d.pages[p] = pg
		}
	}
	m.mu.Lock()
	for a, set := range m.out {
		var bs []int
		for b := range set {
			bs = append(bs, b)
		}
		sort.Ints(bs)
		d.out[a] = bs
	}
	m.mu.Unlock()
	return d
}

func dumpNaive(n *naive) stateDump {
	d := stateDump{
		pages:   map[int]Page{},
		out:     map[int][]int{},
		maxMod:  n.maxModify,
		flushed: n.flushed,
	}
	for _, p := range n.dirtySorted() {
		d.order = append(d.order, OrderEntry{Oldest: n.pages[p].oldest, Page: p})
	}
	d.dirtyN = len(d.order)
	for p, st := range n.pages {
		if st.lsn == 0 {
			continue
		}
		d.pages[p] = Page{
			ID:         p,
			LSN:        st.lsn,
			Oldest:     st.oldest,
			Dirty:      st.dirty,
			InFlight:   st.inFlight,
			FirstAfter: st.firstAfter,
		}
	}
	for a, set := range n.out {
		var bs []int
		for b := range set {
			bs = append(bs, b)
		}
		sort.Ints(bs)
		d.out[a] = bs
	}
	if len(d.order) > 0 {
		d.cp = d.order[0].Oldest
	} else {
		d.cp = n.maxModify + 1
	}
	return d
}

func compareStates(t *testing.T, got, want stateDump, where string) {
	t.Helper()
	if fmt.Sprint(got.order) != fmt.Sprint(want.order) {
		t.Fatalf("[%s] order mismatch:\n got %v\nwant %v", where, got.order, want.order)
	}
	if got.dirtyN != want.dirtyN {
		t.Fatalf("[%s] dirtyN %d != %d", where, got.dirtyN, want.dirtyN)
	}
	if got.maxMod != want.maxMod {
		t.Fatalf("[%s] maxModify %d != %d", where, got.maxMod, want.maxMod)
	}
	if got.flushed != want.flushed {
		t.Fatalf("[%s] flushed %d != %d", where, got.flushed, want.flushed)
	}
	if got.cp != want.cp {
		t.Fatalf("[%s] checkpoint %d != %d", where, got.cp, want.cp)
	}
	if fmt.Sprint(got.pages) != fmt.Sprint(want.pages) {
		t.Fatalf("[%s] pages mismatch:\n got %v\nwant %v", where, got.pages, want.pages)
	}
	if fmt.Sprint(got.out) != fmt.Sprint(want.out) {
		t.Fatalf("[%s] edges mismatch:\n got %v\nwant %v", where, got.out, want.out)
	}
}

// TestRandomDifferential 对 2000 组随机操作序列，逐操作对比生产实现与朴素模型：
// 返回结果、拒绝原因，以及每一步的完整状态（链表、页、边、水位、Checkpoint）。
// 每组的完整输入/输出/判定依据写入独立日志文件。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	if testing.Short() {
		t.Skip("skipping differential test in -short mode")
	}
	logDir := os.Getenv("DIRTYFLUSH_DIFFLOG_DIR")
	var logFile *os.File
	if logDir != "" {
		var err error
		if logFile, err = os.CreateTemp(logDir, "difflog-*.txt"); err != nil {
			t.Fatal(err)
		}
	}
	var combined strings.Builder
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 7))
		D := 1 + rng.Intn(8)
		npages := 1 + rng.Intn(12)
		ops := generateOps(rng, npages, 40+rng.Intn(40))
		n := newNaive(D)
		m, err := New(D)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&combined, "=== sequence %d seed=%d D=%d pages=%d ops=%d ===\n", seq, seq, D, npages, len(ops))
		n.log = &combined
		for i, o := range ops {
			where := fmt.Sprintf("seq=%d step=%d op=%s", seq, i, o.op)
			var got outcome
			switch o.op {
			case "Modify":
				err := m.Modify(o.p1, o.lsn)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r}
			case "SetFlushed":
				err := m.SetFlushed(o.lsn)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r}
			case "FlushStart":
				snap, err := m.FlushStart(o.p1)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r, snap: snap}
			case "FlushDone":
				err := m.FlushDone(o.p1)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r}
			case "AddDep":
				err := m.AddDep(o.p1, o.p2)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r}
			case "Checkpoint":
				got = outcome{ok: true, value: m.Checkpoint()}
			case "Plan":
				plan, err := m.Plan(o.lsn)
				ok, r := reasonOf(err)
				got = outcome{ok: ok, reason: r, plan: plan}
			}
			want := n.do(o.op, o.p1, o.p2, o.lsn)
			if got.ok != want.ok || got.reason != want.reason {
				t.Fatalf("[%s] outcome mismatch: got {ok=%v reason=%d} want {ok=%v reason=%d}\n%s",
					where, got.ok, got.reason, want.ok, want.reason, combined.String())
			}
			if o.op == "FlushStart" && got.ok && got.snap != want.snap {
				t.Fatalf("[%s] snap %d != %d\n%s", where, got.snap, want.snap, combined.String())
			}
			if o.op == "Plan" && got.ok && !eqInts(got.plan, want.plan) {
				t.Fatalf("[%s] plan %v != %v\n%s", where, got.plan, want.plan, combined.String())
			}
			if o.op == "Checkpoint" && got.value != want.value {
				t.Fatalf("[%s] cp %d != %d\n%s", where, got.value, want.value, combined.String())
			}
			compareStates(t, dumpState(t, m), dumpNaive(n), where)
			if inv := m.CheckInvariants(); inv != nil {
				t.Fatalf("[%s] invariant broken: %v\n%s", where, inv, combined.String())
			}
		}
		if testing.Verbose() && seq%200 == 0 {
			t.Logf("sequence %d/%d done (D=%d, pages=%d)", seq, sequences, D, npages)
		}
	}
	if logFile != nil {
		if _, err := logFile.WriteString(combined.String()); err != nil {
			t.Fatal(err)
		}
		t.Logf("full differential log (inputs/outputs/reasons) written to %s", logFile.Name())
		logFile.Close()
	}
}
