package spill

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// captureLogger collects each formatted log line.
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureLogger) Printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *captureLogger) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func newTestManager(t *testing.T, memLimit, blockLimit int, log Logger) *Manager {
	t.Helper()
	m, err := New(Config{MaxMemoryRows: memLimit, MaxSpillBlocks: blockLimit, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func rows(prefix string, n int) []Row {
	out := make([]Row, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%d", prefix, i)
	}
	return out
}

func mustCommit(t *testing.T, m *Manager, id int64) []Row {
	t.Helper()
	var got []Row
	err := m.Commit(context.Background(), id, func(r Row) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("Commit(%d): %v", id, err)
	}
	return got
}

func TestInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{
		{MaxMemoryRows: 0, MaxSpillBlocks: 1},
		{MaxMemoryRows: -1, MaxSpillBlocks: 1},
		{MaxMemoryRows: 1, MaxSpillBlocks: 0},
		{MaxMemoryRows: 1, MaxSpillBlocks: -2},
	} {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("cfg=%+v want ErrInvalidConfig, got %v", cfg, err)
		}
	}
}

func TestVictimSelectionMostMemory(t *testing.T) {
	m := newTestManager(t, 100, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 30)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 20)...); err != nil {
		t.Fatal(err)
	}
	// Adding 51 rows to txn 2 pushes total to 101; txn 2 now holds 71 rows, so
	// it must be the spilled victim.
	if err := m.Append(2, rows("c", 51)...); err != nil {
		t.Fatal(err)
	}
	blocks := m.QueryBlocks()
	if len(blocks) != 1 || blocks[0].TxnID != 2 || len(blocks[0].Rows) != 71 {
		t.Fatalf("want one 71-row block for txn 2, got %+v", blocks)
	}
	if got := m.MemoryRows(); got != 30 {
		t.Fatalf("memory rows = %d, want 30", got)
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestVictimCanBeOtherTransaction(t *testing.T) {
	m := newTestManager(t, 100, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 60)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 10)...); err != nil {
		t.Fatal(err)
	}
	// txn 2 grows by 35 (to 45); txn 1 still leads with 60 and is spilled.
	if err := m.Append(2, rows("c", 35)...); err != nil {
		t.Fatal(err)
	}
	blocks := m.QueryBlocks()
	if len(blocks) != 1 || blocks[0].TxnID != 1 || len(blocks[0].Rows) != 60 {
		t.Fatalf("want one 60-row block for txn 1, got %+v", blocks)
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestVictimSelectionTieSmallestID(t *testing.T) {
	m := newTestManager(t, 100, 100, nil)
	for _, id := range []int64{5, 3, 7} {
		if err := m.Begin(id); err != nil {
			t.Fatal(err)
		}
		if err := m.Append(id, rows(fmt.Sprintf("t%d", id), 40)...); err != nil {
			t.Fatal(err)
		}
	}
	// Each txn has 40 rows (total 120). One more row forces a spill; the three
	// are tied at 40 so txn 3 wins.
	if err := m.Append(5, "extra"); err != nil {
		t.Fatal(err)
	}
	blocks := m.QueryBlocks()
	if len(blocks) != 1 || blocks[0].TxnID != 3 {
		t.Fatalf("want spill of txn 3 on tie, got %+v", blocks)
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCommitReplayOrder(t *testing.T) {
	var log captureLogger
	m := newTestManager(t, 3, 100, &log)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}

	// Append rows one at a time; with no other txn open the target itself is
	// spilled into multiple blocks. Block order must preserve append order.
	var want []Row
	for i := 0; i < 10; i++ {
		r := Row(fmt.Sprintf("r%d", i))
		want = append(want, r)
		if err := m.Append(1, r); err != nil {
			t.Fatal(err)
		}
		if err := m.Check(); err != nil {
			t.Fatal(err)
		}
	}
	blocks := m.QueryBlocks()
	if len(blocks) < 2 {
		t.Fatalf("expected multiple blocks, got %d", len(blocks))
	}
	for i, b := range blocks {
		if b.Seq != i {
			t.Fatalf("block seq = %d, want %d", b.Seq, i)
		}
		if b.ID <= 0 || (i > 0 && b.ID <= blocks[i-1].ID) {
			t.Fatalf("block ids not strictly ascending: %+v", blocks)
		}
	}

	got := mustCommit(t, m, 1)
	if len(got) != len(want) {
		t.Fatalf("committed %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	if m.BlockCount() != 0 || len(m.QueryTxns()) != 0 {
		t.Fatalf("commit did not clean up: blocks=%d txns=%d", m.BlockCount(), len(m.QueryTxns()))
	}
	if snap := m.LogSnapshot(); len(snap) != 10 {
		t.Fatalf("log snapshot len = %d, want 10", len(snap))
	}
	if !strings.Contains(log.String(), "victim=txn1") ||
		!strings.Contains(log.String(), "memoryRows=") ||
		!strings.Contains(log.String(), "reason=max-memory,min-id") {
		t.Fatalf("log missing step/memory/reason fields:\n%s", log.String())
	}
}

func TestCommitOrderAcrossTransactions(t *testing.T) {
	m := newTestManager(t, 2, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 5)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 5)...); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, m, 2)
	mustCommit(t, m, 1)

	snap := m.LogSnapshot()
	want := append(rows("b", 5), rows("a", 5)...)
	if len(snap) != len(want) {
		t.Fatalf("log len = %d, want %d", len(snap), len(want))
	}
	for i := range want {
		if snap[i] != want[i] {
			t.Fatalf("log[%d]=%q want %q (downstream must see commit order)", i, snap[i], want[i])
		}
	}
}

func TestRollbackCleanup(t *testing.T) {
	m := newTestManager(t, 2, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 6)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 3)...); err != nil {
		t.Fatal(err)
	}
	if n := m.BlockCount(); n == 0 {
		t.Fatal("expected blocks before rollback")
	}

	var out []Row
	sink := func(r Row) error { out = append(out, r); return nil }
	if err := m.Rollback(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(context.Background(), 2, sink); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("rollback leaked rows to output: %v", out)
	}
	for _, b := range m.QueryBlocks() {
		if b.TxnID == 1 {
			t.Fatalf("rollback left block behind: %+v", b)
		}
	}
	if len(m.LogSnapshot()) != 3 {
		t.Fatal("rolled-back rows must not reach the log")
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreFullRejectsBeforeAnySpill(t *testing.T) {
	// cap 3, block cap 1. Fill the only slot legitimately (txn 1 packed when
	// txn 2 reaches 3 rows); a further append that needs another block is then
	// rejected wholesale, leaving every prior state untouched.
	m := newTestManager(t, 3, 1, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(3); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 3)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 3)...); err != nil {
		t.Fatal(err)
	}
	if m.BlockCount() != 1 || m.MemoryRows() != 3 {
		t.Fatalf("setup: blocks=%d mem=%d", m.BlockCount(), m.MemoryRows())
	}
	before := m.QueryTxns()
	err := m.Append(3, rows("x", 5)...)
	if !errors.Is(err, ErrStoreFull) {
		t.Fatalf("want ErrStoreFull, got %v", err)
	}
	after := m.QueryTxns()
	if len(after) != len(before) {
		t.Fatalf("rejected append changed open txns: %+v -> %+v", before, after)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("rejected append changed txn state: %+v -> %+v", before[i], after[i])
		}
	}
	if m.BlockCount() != 1 || m.MemoryRows() != 3 || len(m.LogSnapshot()) != 0 {
		t.Fatalf("rejected append left traces: blocks=%d mem=%d", m.BlockCount(), m.MemoryRows())
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectionsLeaveStateUntouched(t *testing.T) {
	m := newTestManager(t, 10, 2, nil)
	if err := m.Begin(7); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(7, rows("a", 4)...); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		return fmt.Sprintf("mem=%d blocks=%d txns=%d log=%d",
			m.MemoryRows(), m.BlockCount(), len(m.QueryTxns()), len(m.LogSnapshot()))
	}
	before := snapshot()

	check := func(name string, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", name, want, err)
		}
		if after := snapshot(); after != before {
			t.Errorf("%s: state changed %q -> %q", name, before, after)
		}
	}

	check("begin duplicate", m.Begin(7), ErrTxnExists)
	check("begin invalid id", m.Begin(0), ErrInvalidTxn)
	check("begin negative id", m.Begin(-3), ErrInvalidTxn)
	check("append unknown", m.Append(99, "x"), ErrTxnNotFound)
	check("append invalid id", m.Append(0, "x"), ErrInvalidTxn)
	check("append no rows", m.Append(7), ErrInvalidRows)
	check("append empty row", m.Append(7, "ok", ""), ErrInvalidRows)
	check("commit unknown", m.Commit(context.Background(), 99, func(Row) error { return nil }), ErrTxnNotFound)
	check("commit invalid id", m.Commit(context.Background(), 0, func(Row) error { return nil }), ErrInvalidTxn)
	check("rollback unknown", m.Rollback(99), ErrTxnNotFound)
	check("rollback invalid id", m.Rollback(0), ErrInvalidTxn)

	// Fill both block slots gradually (each append may consume at most the
	// capacity that exists at that moment), leaving the store full; a later big
	// append needing another block must be rejected with no state change.
	if err := m.Append(7, rows("b", 15)...); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(7, rows("d", 15)...); err != nil {
		t.Fatal(err)
	}
	if m.BlockCount() != 2 {
		t.Fatalf("setup: blocks = %d, want 2", m.BlockCount())
	}
	before = snapshot()
	txnBefore := m.QueryTxns()[0]
	check("append store full", m.Append(7, rows("c", 50)...), ErrStoreFull)
	if got := m.QueryTxns()[0]; got.TotalRows != txnBefore.TotalRows {
		t.Fatalf("rejected append changed txn totals: %+v -> %+v", txnBefore, got)
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}

	// Operations on an ended transaction report not-found and stay rejected.
	if err := m.Rollback(7); err != nil {
		t.Fatal(err)
	}
	before = snapshot()
	check("append ended", m.Append(7, "x"), ErrTxnNotFound)
	check("commit ended", m.Commit(context.Background(), 7, func(Row) error { return nil }), ErrTxnNotFound)
	check("rollback ended", m.Rollback(7), ErrTxnNotFound)
}

func TestBlockIDsNeverReused(t *testing.T) {
	m := newTestManager(t, 3, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(1, rows("a", 4)...); err != nil {
		t.Fatal(err)
	}
	first := m.QueryBlocks()
	if len(first) != 1 {
		t.Fatalf("blocks = %d, want 1", len(first))
	}
	if first[0].ID != 1 {
		t.Fatalf("first block id = %d, want 1", first[0].ID)
	}
	mustCommit(t, m, 1)

	if err := m.Begin(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(2, rows("b", 8)...); err != nil {
		t.Fatal(err)
	}
	second := m.QueryBlocks()
	if len(second) != 1 {
		t.Fatalf("blocks = %d, want 1", len(second))
	}
	if second[0].ID <= first[len(first)-1].ID {
		t.Fatalf("block id reused: new %d <= old max %d", second[0].ID, first[len(first)-1].ID)
	}
}

// naiveManager is the unlimited reference: every append is buffered, commit
// emits all rows in order, rollback drops everything.
type naiveManager struct {
	txns map[int64][]Row
	open map[int64]bool
	log  []Row
}

func newNaive() *naiveManager {
	return &naiveManager{txns: map[int64][]Row{}, open: map[int64]bool{}}
}

func (n *naiveManager) begin(id int64) bool {
	if id <= 0 || n.open[id] {
		return false
	}
	n.open[id] = true
	n.txns[id] = nil
	return true
}

func (n *naiveManager) append(id int64, rs []Row) bool {
	if id <= 0 || !n.open[id] || len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if r == "" {
			return false
		}
	}
	n.txns[id] = append(n.txns[id], rs...)
	return true
}

func (n *naiveManager) commit(id int64) bool {
	if id <= 0 || !n.open[id] {
		return false
	}
	n.log = append(n.log, n.txns[id]...)
	delete(n.txns, id)
	delete(n.open, id)
	return true
}

func (n *naiveManager) rollback(id int64) bool {
	if id <= 0 || !n.open[id] {
		return false
	}
	delete(n.txns, id)
	delete(n.open, id)
	return true
}

// TestRandomEquivalenceWithNaiveReference drives both the bounded manager and
// the naive reference with identical random schedules and compares the
// committed log. Appends are capped per txn so the spill store never fills
// (rejection reasons are covered separately).
func TestRandomEquivalenceWithNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const memLimit, blockLimit, txns, steps = 4, 500, 6, 4000
	m := newTestManager(t, memLimit, blockLimit, nil)
	ref := newNaive()

	perTxnRows := make([]int, txns)

	for step := 0; step < steps; step++ {
		id := int64(1 + rng.Intn(txns))
		switch rng.Intn(10) {
		case 0, 1:
			gotB := m.Begin(id) == nil
			gotR := ref.begin(id)
			if gotB != gotR {
				t.Fatalf("step %d begin(%d): bounded=%v naive=%v", step, id, gotB, gotR)
			}
		case 2, 3, 4, 5, 6:
			n := 1 + rng.Intn(memLimit)
			// Keep each txn small enough that total blocks stay below the cap.
			if perTxnRows[id-1] >= 30 {
				continue
			}
			if n > 30-perTxnRows[id-1] {
				n = 30 - perTxnRows[id-1]
			}
			batch := rows(fmt.Sprintf("t%d-s%d", id, step), n)
			err := m.Append(id, batch...)
			okR := ref.append(id, batch)
			if (err == nil) != okR {
				t.Fatalf("step %d append(%d,%d): bounded err=%v naive=%v", step, id, n, err, okR)
			}
			if err == nil {
				perTxnRows[id-1] += n
			}
			if m.MemoryRows() > memLimit {
				t.Fatalf("step %d: memory rows %d exceed limit %d", step, m.MemoryRows(), memLimit)
			}
			if m.BlockCount() > blockLimit {
				t.Fatalf("step %d: block count %d exceeds limit", step, m.BlockCount())
			}
			if err := m.Check(); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		case 7, 8:
			var out []Row
			err := m.Commit(context.Background(), id, func(r Row) error { out = append(out, r); return nil })
			okR := ref.commit(id)
			if (err == nil) != okR {
				t.Fatalf("step %d commit(%d): bounded=%v naive=%v", step, id, err, okR)
			}
			if err == nil {
				perTxnRows[id-1] = 0
			}
		default:
			err := m.Rollback(id)
			okR := ref.rollback(id)
			if (err == nil) != okR {
				t.Fatalf("step %d rollback(%d): bounded=%v naive=%v", step, id, err, okR)
			}
			if err == nil {
				perTxnRows[id-1] = 0
			}
		}

		got := m.LogSnapshot()
		if len(got) != len(ref.log) {
			t.Fatalf("step %d: log len %d != naive %d", step, len(got), len(ref.log))
		}
		for i := range ref.log {
			if got[i] != ref.log[i] {
				t.Fatalf("step %d: log row %d = %q, naive %q", step, i, got[i], ref.log[i])
			}
		}
	}

	// Drain remaining open txns (in id order) and compare one last time.
	for id := int64(1); id <= txns; id++ {
		var out []Row
		err := m.Commit(context.Background(), id, func(r Row) error { out = append(out, r); return nil })
		if err == nil {
			ref.commit(id)
		}
	}
	got := m.LogSnapshot()
	if len(got) != len(ref.log) {
		t.Fatalf("final log len %d != naive %d", len(got), len(ref.log))
	}
	for i := range ref.log {
		if got[i] != ref.log[i] {
			t.Fatalf("final log row %d = %q, naive %q", i, got[i], ref.log[i])
		}
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	const memLimit, blockLimit, writers, appends = 8, 1000, 8, 100
	m := newTestManager(t, memLimit, blockLimit, nil)

	var wg sync.WaitGroup

	// Readers hammer the downstream log, block query, txn query and self-check
	// concurrently with append/commit traffic.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < appends*writers/2; j++ {
				if m.MemoryRows() > memLimit {
					t.Errorf("memory rows %d exceed limit", m.MemoryRows())
					return
				}
				if m.BlockCount() > blockLimit {
					t.Errorf("blocks %d exceed limit", m.BlockCount())
					return
				}
				_ = m.QueryBlocks()
				_ = m.QueryTxns()
				_ = m.LogSnapshot()
				if err := m.Check(); err != nil {
					t.Errorf("check: %v", err)
					return
				}
			}
		}()
	}

	// Each writer owns one txn, appends rows (which may spill other txns), then
	// commits; output order is checked per txn.
	for w := int64(1); w <= writers; w++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if err := m.Begin(id); err != nil {
				t.Errorf("begin %d: %v", id, err)
				return
			}
			expected := map[int64][]Row{}
			_ = expected
			var want []Row
			for j := 0; j < appends; j++ {
				r := fmt.Sprintf("txn%d-row%d", id, j)
				if err := m.Append(id, r); err != nil {
					t.Errorf("append %d/%d: %v", id, j, err)
					return
				}
				want = append(want, r)
			}
			var got []Row
			err := m.Commit(context.Background(), id, func(r Row) error {
				got = append(got, r)
				return nil
			})
			if err != nil {
				t.Errorf("commit %d: %v", id, err)
				return
			}
			if len(got) != len(want) {
				t.Errorf("txn %d: got %d rows, want %d", id, len(got), len(want))
				return
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("txn %d row %d = %q want %q", id, i, got[i], want[i])
					return
				}
			}
		}(w)
	}

	wg.Wait()

	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
	if n := len(m.LogSnapshot()); n != writers*appends {
		t.Fatalf("committed log rows = %d, want %d", n, writers*appends)
	}
	if m.BlockCount() != 0 || len(m.QueryTxns()) != 0 {
		t.Fatalf("residue: blocks=%d openTxns=%d", m.BlockCount(), len(m.QueryTxns()))
	}
}

func TestCommitSinkFailureLeavesTransactionOpen(t *testing.T) {
	m := newTestManager(t, 2, 100, nil)
	if err := m.Begin(1); err != nil {
		t.Fatal(err)
	}
	want := rows("a", 6)
	if err := m.Append(1, want...); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("downstream boom")
	calls := 0
	err := m.Commit(context.Background(), 1, func(Row) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want sink error, got %v", err)
	}
	if len(m.LogSnapshot()) != 0 {
		t.Fatal("failed commit must not publish to log")
	}
	if len(m.QueryTxns()) != 1 {
		t.Fatal("failed commit must leave txn open")
	}
	// Retry commit succeeds with full append-order output.
	got := mustCommit(t, m, 1)
	if len(got) != len(want) {
		t.Fatalf("retry committed %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retry row %d = %q want %q", i, got[i], want[i])
		}
	}
}
