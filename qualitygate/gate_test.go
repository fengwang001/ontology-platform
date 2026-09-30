package qualitygate

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	// MinRows 100; null ratio <= 1/10; K 3; band [80%,120%]; M 3.
	return Config{
		MinRows:    100,
		MaxNullNum: 1,
		MaxNullDen: 10,
		BaselineK:  3,
		LoPercent:  20,
		HiPercent:  20,
		MaxBlocks:  3,
	}
}

// lineLogger mirrors every audit line into the test log and a buffer.
type lineLogger struct {
	t   *testing.T
	buf *bytes.Buffer
	mu  sync.Mutex
}

func (l *lineLogger) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.buf.WriteString(line + "\n")
	l.mu.Unlock()
	l.t.Log(line)
}

func newTestGate(t *testing.T) (*Gate, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	g, err := New(testConfig(), &lineLogger{t: t, buf: &buf})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, &buf
}

func mustSubmit(t *testing.T, g *Gate, b Batch) *AdjudicationResult {
	t.Helper()
	res, err := g.Submit(b)
	if err != nil {
		t.Fatalf("Submit %+v: %v", b, err)
	}
	return res
}

// Out-of-order arrivals: the baseline is by seq position, not arrival order.
func TestBaselineBySeqNotArrivalOrder(t *testing.T) {
	g, _ := newTestGate(t)

	// Seq 3 arrives first; no eligible baseline -> rule three not evaluated.
	r3 := mustSubmit(t, g, Batch{Seq: 3, Rows: 500, Nulls: 0})
	if r3.Decision != DecisionPassed || r3.Baseline != 0 {
		t.Fatalf("seq3 = %+v, want passed without baseline", r3)
	}

	// Seq 1 later: seq 3 has a larger seq and cannot be its baseline.
	r1 := mustSubmit(t, g, Batch{Seq: 1, Rows: 1000, Nulls: 0})
	if r1.Decision != DecisionPassed || r1.Baseline != 0 {
		t.Fatalf("seq1 = %+v, want passed without baseline", r1)
	}

	// Seq 2: only seq 1 eligible; median 1000. 799*100 < 1000*80 -> warning.
	r2 := mustSubmit(t, g, Batch{Seq: 2, Rows: 799, Nulls: 0})
	if r2.Decision != DecisionWarning || r2.Baseline != 1000 ||
		len(r2.BaselineSeqs) != 1 || r2.BaselineSeqs[0] != 1 {
		t.Fatalf("seq2 = %+v, want warning baseline 1000 from seq1", r2)
	}

	// Seq 4: eligible seqs 1(1000),2(799),3(500); median 799 band [639,958].
	r4 := mustSubmit(t, g, Batch{Seq: 4, Rows: 900, Nulls: 0})
	if r4.Decision != DecisionPassed || r4.Baseline != 799 {
		t.Fatalf("seq4 = %+v, want passed baseline 799", r4)
	}
}

// K limits the baseline to the at most K seq-largest released batches.
func TestBaselineKLimitsToLargestSeqs(t *testing.T) {
	g, _ := newTestGate(t)
	mustSubmit(t, g, Batch{Seq: 1, Rows: 100, Nulls: 0})
	mustSubmit(t, g, Batch{Seq: 2, Rows: 110, Nulls: 0})
	mustSubmit(t, g, Batch{Seq: 3, Rows: 400, Nulls: 0})
	mustSubmit(t, g, Batch{Seq: 4, Rows: 500, Nulls: 0})

	// K=3 picks seqs 2,3,4: rows [110,400,500], median 400.
	r := mustSubmit(t, g, Batch{Seq: 5, Rows: 400, Nulls: 0})
	if r.Baseline != 400 || len(r.BaselineSeqs) != 3 {
		t.Fatalf("seq5 baseline=%d seqs=%v, want 400 from [2 3 4]", r.Baseline, r.BaselineSeqs)
	}
}

// Even sample count uses the lower median at index (c-1)/2.
func TestBaselineEvenCountLowerMedian(t *testing.T) {
	cfg := testConfig()
	cfg.BaselineK = 2
	var buf bytes.Buffer
	g, err := New(cfg, &lineLogger{t: t, buf: &buf})
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, g, Batch{Seq: 1, Rows: 100, Nulls: 0})
	mustSubmit(t, g, Batch{Seq: 2, Rows: 300, Nulls: 0})

	r := mustSubmit(t, g, Batch{Seq: 3, Rows: 100, Nulls: 0})
	// rows sorted [100,300], index (2-1)/2 = 0 -> 100.
	if r.Baseline != 100 {
		t.Fatalf("lower median = %d, want 100", r.Baseline)
	}
}

// Exact boundary equality never violates rules one, two or three.
func TestExactBoundariesNotViolations(t *testing.T) {
	// Gate g1 has no baseline, isolating the rule-one boundary: rows ==
	// MinRows passes.
	g1, _ := newTestGate(t)
	if r := mustSubmit(t, g1, Batch{Seq: 1, Rows: 100, Nulls: 0}); r.Decision != DecisionPassed {
		t.Fatalf("rows==MinRows with no baseline: %+v", r)
	}

	// Gate g2 has one baseline sample of 1000 so the band [800,1200] never
	// interferes with the rule-one boundary checks.
	g2, _ := newTestGate(t)
	mustSubmit(t, g2, Batch{Seq: 1, Rows: 1000, Nulls: 0})

	// nulls/rows == 1/10: cross products equal -> passes.
	if r := mustSubmit(t, g2, Batch{Seq: 3, Rows: 1000, Nulls: 100}); r.Decision != DecisionPassed {
		t.Fatalf("null ratio at boundary: %+v", r)
	}

	// Baseline 1000 -> band [800,1200]; exact edges are not violations.
	if r := mustSubmit(t, g2, Batch{Seq: 4, Rows: 800, Nulls: 0}); r.Decision != DecisionPassed {
		t.Fatalf("rows at low boundary: %+v", r)
	}
	if r := mustSubmit(t, g2, Batch{Seq: 5, Rows: 1200, Nulls: 0}); r.Decision != DecisionPassed {
		t.Fatalf("rows at high boundary: %+v", r)
	}

	// 70 rows both under MinRows and (9*10 > 70*1): blocked; both blocking
	// rules listed first in rule order (all failed rules are reported).
	r := mustSubmit(t, g2, Batch{Seq: 6, Rows: 70, Nulls: 9})
	if r.Decision != DecisionBlocked || len(r.Violations) != 3 ||
		r.Violations[0] != RuleRowsBelowMin ||
		r.Violations[1] != RuleNullRatio ||
		r.Violations[2] != RuleBaselineDev {
		t.Fatalf("seq6 = %+v, want blocked with all three rules in order", r)
	}
}

// Rule two is skipped when rows == 0; only rule one blocks.
func TestZeroRowsSkipsNullRatio(t *testing.T) {
	g, _ := newTestGate(t)
	r := mustSubmit(t, g, Batch{Seq: 1, Rows: 0, Nulls: 0})
	if r.Decision != DecisionBlocked || len(r.Violations) != 1 ||
		r.Violations[0] != RuleRowsBelowMin {
		t.Fatalf("zero rows = %+v", r)
	}
}

// A manually released quarantine batch enters later seqs' baseline.
func TestManualReleaseEntersBaseline(t *testing.T) {
	g, _ := newTestGate(t)

	r1 := mustSubmit(t, g, Batch{Seq: 1, Rows: 50, Nulls: 0})
	if r1.Decision != DecisionBlocked {
		t.Fatalf("seq1 = %+v", r1)
	}
	if err := g.ManuallyRelease(1); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Baseline 50 -> band [40,60]; 100 clears MinRows and exceeds the high
	// edge, so it is a warning-only pass that still proves the baseline use.
	r2 := mustSubmit(t, g, Batch{Seq: 2, Rows: 100, Nulls: 0})
	if r2.Decision != DecisionWarning || r2.Baseline != 50 ||
		len(r2.BaselineSeqs) != 1 || r2.BaselineSeqs[0] != 1 {
		t.Fatalf("seq2 = %+v, want warning vs released seq1 baseline 50", r2)
	}
	snap, err := g.Query(1)
	if err != nil || snap.Status != StatusReleased {
		t.Fatalf("query seq1 = %+v err=%v, want released", snap, err)
	}
}

// Consecutive blocks pause at M; passes clear the streak; resume resets all.
func TestConsecutiveBlocksPauseAndResume(t *testing.T) {
	g, _ := newTestGate(t)
	block := func(seq int) *AdjudicationResult {
		return mustSubmit(t, g, Batch{Seq: seq, Rows: 1, Nulls: 0})
	}

	block(1)
	mustSubmit(t, g, Batch{Seq: 2, Rows: 100, Nulls: 0}) // pass clears streak
	if g.BlockStreak() != 0 {
		t.Fatalf("streak after pass = %d, want 0", g.BlockStreak())
	}

	block(3)
	b4 := block(4)
	if b4.BlockStreak != 2 || b4.Paused {
		t.Fatalf("after blocks 3,4: %+v", b4)
	}
	b5 := block(5) // third consecutive block -> M reached
	if b5.BlockStreak != 3 || !b5.Paused || !g.Paused() {
		t.Fatalf("after block5: %+v", b5)
	}

	// Paused: submit/resubmit rejected without adjudication.
	if _, err := g.Submit(Batch{Seq: 6, Rows: 100}); !errors.Is(err, ErrPaused) {
		t.Fatalf("submit while paused err=%v, want ErrPaused", err)
	}
	if _, err := g.Resubmit(Batch{Seq: 3, Rows: 100}); !errors.Is(err, ErrPaused) {
		t.Fatalf("resubmit while paused err=%v, want ErrPaused", err)
	}

	// Release/discard stay available while paused and never touch the streak.
	if err := g.ManuallyRelease(4); err != nil {
		t.Fatalf("release while paused: %v", err)
	}
	if err := g.Discard(5); err != nil {
		t.Fatalf("discard while paused: %v", err)
	}
	if g.BlockStreak() != 3 || !g.Paused() {
		t.Fatal("release/discard must leave streak and pause untouched")
	}

	if err := g.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if g.Paused() || g.BlockStreak() != 0 {
		t.Fatalf("after resume paused=%v streak=%d", g.Paused(), g.BlockStreak())
	}
	if err := g.Resume(); !errors.Is(err, ErrNotPaused) {
		t.Fatalf("second resume err=%v, want ErrNotPaused", err)
	}
}

// Resubmit readjudicates with the baseline computed at the same seq position;
// an earlier-arriving larger seq must not leak into that baseline.
func TestResubmitAndNoRecompute(t *testing.T) {
	g, _ := newTestGate(t)

	// Seq 2 arrives first, blocks, enters quarantine.
	r2 := mustSubmit(t, g, Batch{Seq: 2, Rows: 1, Nulls: 0})
	if r2.Decision != DecisionBlocked {
		t.Fatalf("seq2 = %+v", r2)
	}
	// Seq 1 arrives and passes; it is smaller seq than 2, so a resubmit of 2
	// must now use it as baseline.
	mustSubmit(t, g, Batch{Seq: 1, Rows: 500, Nulls: 0})

	rr, err := g.Resubmit(Batch{Seq: 2, Rows: 100, Nulls: 0})
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	// 100 vs baseline 500 (band [400,600]) -> warning pass; streak cleared.
	if rr.Decision != DecisionWarning || rr.Baseline != 500 {
		t.Fatalf("resubmitted seq2 = %+v, want warning baseline 500", rr)
	}
	if g.BlockStreak() != 0 {
		t.Fatalf("streak after warning pass = %d, want 0", g.BlockStreak())
	}

	// A blocked resubmission of another quarantined batch counts in the streak.
	mustSubmit(t, g, Batch{Seq: 10, Rows: 1, Nulls: 0})
	rb2, err := g.Resubmit(Batch{Seq: 10, Rows: 1, Nulls: 0}) // still below MinRows
	if err != nil || rb2.Decision != DecisionBlocked {
		t.Fatalf("second resubmit res=%+v err=%v, want blocked", rb2, err)
	}
	if g.BlockStreak() != 2 {
		t.Fatalf("streak = %d, want 2 (block10 + blocked resubmit)", g.BlockStreak())
	}

	// An unknown seq is distinguishable from any quarantine-state error.
	if _, err := g.Query(3); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("query unknown seq err=%v, want ErrSeqNotFound", err)
	}
}

// Rejections follow the mandated precedence and stay distinguishable, and a
// rejected operation must not mutate any state.
func TestRejectionReasonsAndNoStateChange(t *testing.T) {
	g, _ := newTestGate(t)

	// Submit precedence: paused (checked separately below), invalid, exists.
	for _, b := range []Batch{
		{Seq: 0, Rows: 1, Nulls: 0},
		{Seq: 1, Rows: -1, Nulls: 0},
		{Seq: 1, Rows: 10, Nulls: -1},
		{Seq: 1, Rows: 10, Nulls: 11},
	} {
		if _, err := g.Submit(b); !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("submit %+v err=%v, want ErrInvalidBatch", b, err)
		}
	}

	mustSubmit(t, g, Batch{Seq: 1, Rows: 100, Nulls: 0}) // passed
	blocked := mustSubmit(t, g, Batch{Seq: 2, Rows: 1, Nulls: 0})
	if blocked.Decision != DecisionBlocked {
		t.Fatalf("seq2 = %+v", blocked)
	}

	if _, err := g.Submit(Batch{Seq: 1, Rows: 100, Nulls: 0}); !errors.Is(err, ErrSeqExists) {
		t.Fatalf("duplicate submit err=%v, want ErrSeqExists", err)
	}

	// Resubmit precedence: invalid before existence/state.
	if _, err := g.Resubmit(Batch{Seq: -1, Rows: 1}); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("invalid resubmit err=%v, want ErrInvalidBatch", err)
	}
	if _, err := g.Resubmit(Batch{Seq: 9, Rows: 100}); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("resubmit unknown err=%v, want ErrSeqNotFound", err)
	}
	if _, err := g.Resubmit(Batch{Seq: 1, Rows: 100}); !errors.Is(err, ErrAlreadyPassed) {
		t.Fatalf("resubmit passed err=%v, want ErrAlreadyPassed", err)
	}

	// Release/discard precedence: not found, then state; states distinguish.
	if err := g.ManuallyRelease(9); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("release unknown err=%v, want ErrSeqNotFound", err)
	}
	if err := g.Discard(9); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("discard unknown err=%v, want ErrSeqNotFound", err)
	}
	if err := g.ManuallyRelease(1); !errors.Is(err, ErrAlreadyPassed) {
		t.Fatalf("release passed err=%v, want ErrAlreadyPassed", err)
	}

	if err := g.Discard(2); err != nil {
		t.Fatalf("discard seq2: %v", err)
	}
	for _, op := range []func() error{
		func() error { _, e := g.Resubmit(Batch{Seq: 2, Rows: 100}); return e },
		func() error { return g.ManuallyRelease(2) },
		func() error { return g.Discard(2) },
	} {
		err := op()
		if !errors.Is(err, ErrAlreadyDiscarded) {
			t.Fatalf("op on discarded err=%v, want ErrAlreadyDiscarded", err)
		}
		if !errors.Is(err, ErrNotQuarantined) {
			t.Fatalf("discarded err %v must also match ErrNotQuarantined", err)
		}
	}

	// Streak untouched by invalid/release/discard/rejected operations.
	if g.BlockStreak() != 1 {
		t.Fatalf("streak = %d, want 1", g.BlockStreak())
	}
}

// Discard then a smaller-seq release: discarded batch never returns and the
// released one is still usable.
func TestDiscardIsTerminal(t *testing.T) {
	g, _ := newTestGate(t)
	mustSubmit(t, g, Batch{Seq: 1, Rows: 1, Nulls: 0})
	if err := g.Discard(1); err != nil {
		t.Fatalf("discard: %v", err)
	}
	snap, err := g.Query(1)
	if err != nil || snap.Status != StatusDiscarded {
		t.Fatalf("snap = %+v err=%v", snap, err)
	}
	// Resubmitting a discarded seq is refused, so Submit must also see it as
	// an existing seq rather than create a fresh record.
	if _, err := g.Submit(Batch{Seq: 1, Rows: 100, Nulls: 0}); !errors.Is(err, ErrSeqExists) {
		t.Fatalf("submit discarded seq err=%v, want ErrSeqExists", err)
	}
}

// Concurrent operations are serializable: no lost streak updates and once the
// pause is visible, no submission is adjudicated.
func TestConcurrentSerializable(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocks = 5
	var buf bytes.Buffer
	g, err := New(cfg, &lineLogger{t: t, buf: &buf})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	// 5 guaranteed blocks race with queries and release/discard/resume ops.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for seq := 1; seq <= 5; seq++ {
			g.Submit(Batch{Seq: seq, Rows: 1, Nulls: 0})
		}
	}()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = g.Query(1 + i%5)
			_ = g.Paused()
			_ = g.BlockStreak()
		}(i)
	}
	wg.Wait()

	if !g.Paused() || g.BlockStreak() != 5 {
		t.Fatalf("after concurrent blocks paused=%v streak=%d, want paused/5", g.Paused(), g.BlockStreak())
	}

	// After the pause, concurrent late submissions must all be rejected and
	// must not create records.
	start := make(chan struct{})
	errs := make(chan error, 16)
	for seq := 100; seq < 116; seq++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			<-start
			_, err := g.Submit(Batch{Seq: seq, Rows: 100, Nulls: 0})
			errs <- err
		}(seq)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrPaused) {
			t.Fatalf("post-pause submit err=%v, want ErrPaused", err)
		}
	}
	for seq := 100; seq < 116; seq++ {
		if _, err := g.Query(seq); !errors.Is(err, ErrSeqNotFound) {
			t.Fatalf("rejected submit seq=%d must leave no record, err=%v", seq, err)
		}
	}
}

// Audit log lines carry input, output and the decision basis.
func TestAuditLogContents(t *testing.T) {
	g, buf := newTestGate(t)
	mustSubmit(t, g, Batch{Seq: 1, Rows: 100, Nulls: 0})
	mustSubmit(t, g, Batch{Seq: 2, Rows: 200, Nulls: 0}) // median of [100,200] = 100
	mustSubmit(t, g, Batch{Seq: 3, Rows: 200, Nulls: 0}) // baseline seqs 1,2 median 100
	// Baseline median 200 (seqs 1,2,3 -> rows [100,200,200]); band [160,240].
	mustSubmit(t, g, Batch{Seq: 4, Rows: 159, Nulls: 0}) // warning only
	_, _ = g.Submit(Batch{Seq: 0, Rows: 1})              // rejected

	logText := buf.String()
	for _, want := range []string{
		"input=Submit",
		"output=",
		"baseline=",
		"seqs=",
		"violations=",
		"reason=warning",
		"reject",
		ErrInvalidBatch.Error(),
	} {
		if !strings.Contains(logText, want) {
			t.Errorf("audit log missing %q\n%s", want, logText)
		}
	}
}
