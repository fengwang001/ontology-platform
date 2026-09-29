package pitr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

type memLogger struct {
	mu    sync.Mutex
	lines []string
}

func (m *memLogger) Log(kind string, fields map[string]any) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	line := kind
	for _, k := range keys {
		line += fmt.Sprintf(" %s=%v", k, fields[k])
	}
	m.mu.Lock()
	m.lines = append(m.lines, line)
	m.mu.Unlock()
}

func (m *memLogger) contains(sub string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

var bg = context.Background()

func seg(tli, start, end int64) Segment {
	return Segment{TLI: tli, Start: start, End: end}
}

func mustArchive(t *testing.T, r *Registry, segs ...Segment) {
	t.Helper()
	if err := r.ArchiveSegments(bg, segs...); err != nil {
		t.Fatalf("archive: %v", err)
	}
}

func mustTimeline(t *testing.T, r *Registry, id, parent, fork int64) {
	t.Helper()
	if err := r.RegisterTimeline(bg, Timeline{ID: id, Parent: parent, Fork: fork}); err != nil {
		t.Fatalf("register timeline %d: %v", id, err)
	}
}

func mustBackup(t *testing.T, r *Registry, id string, tli, upper int64) {
	t.Helper()
	if err := r.RegisterBackups(bg, Backup{ID: id, TLI: tli, Upper: upper}); err != nil {
		t.Fatalf("register backup %s: %v", id, err)
	}
}

// threeLevelSetup creates tli 1 (fork 0), tli 2 (fork 10), tli 3 (fork 20).
// Each timeline archives its own [fork,30) history; backups b1@tli1:5 and
// b2@tli2:15 exist.
func threeLevelSetup(t *testing.T) (*Registry, *memLogger) {
	t.Helper()
	log := &memLogger{}
	r := NewRegistry().WithLogger(log)
	mustTimeline(t, r, 2, 1, 10)
	mustTimeline(t, r, 3, 2, 20)
	mustArchive(t, r, seg(1, 0, 30), seg(2, 10, 30), seg(3, 20, 30))
	mustBackup(t, r, "b1", 1, 5)
	mustBackup(t, r, "b2", 2, 15)
	return r, log
}

func step(tli, lo, hi int64) ReplayStep {
	return ReplayStep{Seg: seg(tli, 0, 0), Start: lo, End: hi}
}

func assertSteps(t *testing.T, p Plan, want []ReplayStep) {
	t.Helper()
	if len(p.Steps) != len(want) {
		t.Fatalf("steps = %v, want %v", p.Steps, want)
	}
	for i, w := range want {
		got := p.Steps[i]
		if got.Seg.TLI != w.Seg.TLI || got.Start != w.Start || got.End != w.End {
			t.Fatalf("step %d = tli%d [%d,%d), want tli%d [%d,%d)",
				i, got.Seg.TLI, got.Start, got.End, w.Seg.TLI, w.Start, w.End)
		}
	}
}

func TestThreeLevelForkAndEffectiveChain(t *testing.T) {
	r, _ := threeLevelSetup(t)

	p, err := r.PlanRecovery(bg, 3, 24, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Backup.ID != "b2" {
		t.Fatalf("backup = %s, want b2", p.Backup.ID)
	}
	assertSteps(t, p, []ReplayStep{step(2, 15, 20), step(3, 20, 24)})

	res, err := r.Recover(bg, 3, 24, false)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	direct, err := r.DirectState(bg, 3, 24)
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	if res.State != direct {
		t.Fatalf("state %d != direct %d", res.State, direct)
	}
	if res.NewTLI != 4 || res.Parent != 3 || res.Fork != 24 {
		t.Fatalf("new timeline = %d parent %d fork %d, want 4/3/24",
			res.NewTLI, res.Parent, res.Fork)
	}
}

func TestForkInsideSegment(t *testing.T) {
	// tli 3 forks at 23, inside tli-2's [10,30) segment; the segment must be
	// clipped at the fork boundary into two steps.
	log := &memLogger{}
	r := NewRegistry().WithLogger(log)
	mustTimeline(t, r, 2, 1, 10)
	mustTimeline(t, r, 3, 2, 23)
	mustArchive(t, r, seg(1, 0, 30), seg(2, 10, 30), seg(3, 23, 30))
	mustBackup(t, r, "b2", 2, 15)

	p, err := r.PlanRecovery(bg, 3, 28, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	assertSteps(t, p, []ReplayStep{step(2, 15, 23), step(3, 23, 28)})

	res, err := r.Recover(bg, 3, 28, false)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	direct, _ := r.DirectState(bg, 3, 28)
	if res.State != direct {
		t.Fatalf("state %d != direct %d", res.State, direct)
	}
	for _, sub := range []string{"plan_input", "plan_ok", "recover_ok", "backup=b2"} {
		if !log.contains(sub) {
			t.Fatalf("missing decision log %q in %v", sub, log.lines)
		}
	}
}

func TestOldTimelineSegmentAfterForkNotUsable(t *testing.T) {
	// tli 3 forks at 20; tli 2 keeps archiving on the old branch [20,40),
	// tli 3 only has [20,25). The tli-2 bytes must never serve tli 3.
	log := &memLogger{}
	r := NewRegistry().WithLogger(log)
	mustTimeline(t, r, 2, 1, 10)
	mustTimeline(t, r, 3, 2, 20)
	mustArchive(t, r, seg(1, 0, 30), seg(2, 10, 40), seg(3, 20, 25))
	mustBackup(t, r, "b2", 2, 15)

	if _, err := r.PlanRecovery(bg, 3, 26, false); !errors.Is(err, ErrTargetBeyondArchive) {
		t.Fatalf("err = %v, want beyond archive", err)
	}

	// With a hole inside the tli-3 window but tli-2 bytes underneath, the
	// first missing tli-3 position is a gap.
	l2 := &memLogger{}
	r2 := NewRegistry().WithLogger(l2)
	mustTimeline(t, r2, 2, 1, 10)
	mustTimeline(t, r2, 3, 2, 20)
	mustArchive(t, r2, seg(1, 0, 30), seg(2, 10, 40),
		seg(3, 20, 24), seg(3, 26, 30))
	mustBackup(t, r2, "b2", 2, 15)
	_, err := r2.PlanRecovery(bg, 3, 28, false)
	var gap *GapError
	if !errors.As(err, &gap) || gap.Position != 24 {
		t.Fatalf("err = %v, want first gap at 24", err)
	}
	if !l2.contains("position=24") {
		t.Fatalf("gap log must name position 24: %v", l2.lines)
	}
}

func TestTargetModeBoundaries(t *testing.T) {
	r, _ := threeLevelSetup(t)

	// Exclusive target 20: end == 20 lands on the fork, no tli-3 byte.
	re, err := r.Recover(bg, 3, 20, false)
	if err != nil {
		t.Fatalf("exclusive recover: %v", err)
	}
	if re.Fork != 20 || re.Plan.End != 20 {
		t.Fatalf("exclusive fork/end = %d/%d, want 20/20", re.Fork, re.Plan.End)
	}
	assertSteps(t, re.Plan, []ReplayStep{step(2, 15, 20)})
	de, _ := r.DirectState(bg, 3, 20)
	if re.State != de {
		t.Fatalf("exclusive state %d != direct %d", re.State, de)
	}

	// Inclusive target 20: end == 21, exactly one tli-3 byte replayed.
	ri, err := r.Recover(bg, 3, 20, true)
	if err != nil {
		t.Fatalf("inclusive recover: %v", err)
	}
	if ri.Fork != 21 || ri.Plan.End != 21 {
		t.Fatalf("inclusive fork/end = %d/%d, want 21/21", ri.Fork, ri.Plan.End)
	}
	assertSteps(t, ri.Plan, []ReplayStep{step(2, 15, 20), step(3, 20, 21)})
	di, _ := r.DirectState(bg, 3, 21)
	if ri.State != di {
		t.Fatalf("inclusive state %d != direct %d", ri.State, di)
	}

	if re.NewTLI != 4 || ri.NewTLI != 5 {
		t.Fatalf("ids = %d,%d, want 4,5", re.NewTLI, ri.NewTLI)
	}
}

func TestBackupTieBreaksByTimelineID(t *testing.T) {
	// At fork boundary 10 both windows admit upper==10; the greater tli wins.
	r, _ := threeLevelSetup(t)
	mustBackup(t, r, "c1", 1, 10)
	mustBackup(t, r, "c2", 2, 10)

	p, err := r.PlanRecovery(bg, 3, 10, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Backup.ID != "c2" {
		t.Fatalf("backup = %s, want c2 (tli 2 wins the upper tie)", p.Backup.ID)
	}

	// A strictly greater upper dominates regardless of timeline number.
	p2, err := r.PlanRecovery(bg, 3, 18, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p2.Backup.ID != "b2" {
		t.Fatalf("backup = %s, want b2 (upper 15 greatest)", p2.Backup.ID)
	}
}

func TestErrorPriorityAndGapPosition(t *testing.T) {
	r, _ := threeLevelSetup(t)

	if _, err := r.PlanRecovery(bg, 99, 100, false); !errors.Is(err, ErrTimelineNotFound) {
		t.Fatalf("missing tli: %v", err)
	}
	if _, err := r.PlanRecovery(bg, 3, 50, false); !errors.Is(err, ErrTargetBeyondArchive) {
		t.Fatalf("beyond archive: %v", err)
	}

	noBackup := NewRegistry()
	mustArchive(t, noBackup, seg(1, 0, 30))
	if _, err := noBackup.PlanRecovery(bg, 1, 20, false); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("no backup: %v", err)
	}

	gapped := NewRegistry()
	mustArchive(t, gapped, seg(1, 0, 10), seg(1, 12, 30))
	mustBackup(t, gapped, "b", 1, 0)
	var gap *GapError
	_, err := gapped.PlanRecovery(bg, 1, 20, false)
	if !errors.As(err, &gap) || gap.Position != 10 {
		t.Fatalf("err = %v, want first gap at 10", err)
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	r, _ := threeLevelSetup(t)
	before := r.SnapshotAt(bg)

	if err := r.RegisterTimeline(bg, Timeline{ID: 4, Parent: 3, Fork: 15}); !errors.Is(err, ErrInvalidFork) {
		t.Fatalf("fork before parent fork: %v", err)
	}
	if err := r.RegisterTimeline(bg, Timeline{ID: 5, Parent: 99, Fork: 1}); !errors.Is(err, ErrInvalidFork) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := r.Recover(bg, 99, 10, false); !errors.Is(err, ErrTimelineNotFound) {
		t.Fatalf("recover missing tli: %v", err)
	}
	if _, err := r.Recover(bg, 3, 99, false); !errors.Is(err, ErrTargetBeyondArchive) {
		t.Fatalf("recover beyond: %v", err)
	}

	after := r.SnapshotAt(bg)
	if after.MaxTLI != before.MaxTLI ||
		len(after.Timelines) != len(before.Timelines) ||
		len(after.Backups) != len(before.Backups) {
		t.Fatalf("catalog mutated by a rejected operation")
	}
}

func TestConcurrentRecoverDistinctConsecutiveIDs(t *testing.T) {
	r, _ := threeLevelSetup(t)

	const n = 40
	var wg sync.WaitGroup
	results := make(chan TimelineID, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Recover(bg, 3, 24, false)
			if err != nil {
				errs <- err
				return
			}
			results <- res.NewTLI
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent recover: %v", err)
	}

	got := make([]TimelineID, 0, n)
	for id := range results {
		got = append(got, id)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != n {
		t.Fatalf("got %d ids, want %d", len(got), n)
	}
	for i := range got {
		want := TimelineID(4 + i)
		if got[i] != want {
			t.Fatalf("ids not consecutive: got %v, first gap want %d", got, want)
		}
	}
	if r.SnapshotAt(bg).MaxTLI != 3+n {
		t.Fatalf("maxTLI = %d, want %d", r.SnapshotAt(bg).MaxTLI, 3+n)
	}
}

func TestConcurrentArchivePlanRecover(t *testing.T) {
	r, _ := threeLevelSetup(t)

	const n = 30
	var wg sync.WaitGroup
	wg.Add(3 * n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = r.PlanRecovery(bg, 3, 24, false)
		}()
		go func(i int) {
			defer wg.Done()
			// tli-4 segments beyond 30 never overlap the fixed archive.
			_ = r.ArchiveSegments(bg, Segment{TLI: 1, Start: 30 + int64(i), End: 31 + int64(i)})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = r.Recover(bg, 3, 22, false)
		}()
	}
	wg.Wait()
}

func TestSameInputProducesIdenticalPlan(t *testing.T) {
	r, _ := threeLevelSetup(t)

	p1, err := r.PlanRecovery(bg, 3, 24, true)
	if err != nil {
		t.Fatalf("plan1: %v", err)
	}
	p2, err := r.PlanRecovery(bg, 3, 24, true)
	if err != nil {
		t.Fatalf("plan2: %v", err)
	}
	if !plansEqual(p1, p2) {
		t.Fatalf("plans differ:\n%+v\n%+v", p1, p2)
	}

	// Repeated concurrent planning is item-for-item identical too.
	const n = 20
	plans := make(chan Plan, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := r.PlanRecovery(bg, 3, 24, true)
			if err != nil {
				t.Errorf("plan: %v", err)
				return
			}
			plans <- p
		}()
	}
	wg.Wait()
	close(plans)
	for p := range plans {
		if !plansEqual(p1, p) {
			t.Fatalf("concurrent plan differs:\n%+v\n%+v", p1, p)
		}
	}
}

func plansEqual(a, b Plan) bool {
	if a.TargetTLI != b.TargetTLI || a.Target != b.Target ||
		a.Inclusive != b.Inclusive || a.End != b.End || a.Backup != b.Backup ||
		len(a.Steps) != len(b.Steps) {
		return false
	}
	for i := range a.Steps {
		sa, sb := a.Steps[i], b.Steps[i]
		if sa.Start != sb.Start || sa.End != sb.End || sa.Seg.TLI != sb.Seg.TLI {
			return false
		}
	}
	return true
}

func TestRegistrationValidation(t *testing.T) {
	r, _ := threeLevelSetup(t)

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"segment unknown tli", func() error {
			return r.ArchiveSegments(bg, seg(9, 0, 5))
		}, ErrInvalidSegment},
		{"segment empty interval", func() error {
			return r.ArchiveSegments(bg, seg(1, 10, 10))
		}, ErrInvalidSegment},
		{"segment overlap", func() error {
			return r.ArchiveSegments(bg, seg(1, 25, 28))
		}, ErrInvalidSegment},
		{"backup unknown tli", func() error {
			return r.RegisterBackups(bg, Backup{ID: "x", TLI: 9, Upper: 0})
		}, ErrInvalidBackup},
		{"backup before fork", func() error {
			return r.RegisterBackups(bg, Backup{ID: "x", TLI: 2, Upper: 5})
		}, ErrInvalidBackup},
		{"backup duplicate id", func() error {
			return r.RegisterBackups(bg, Backup{ID: "b1", TLI: 1, Upper: 6})
		}, ErrInvalidBackup},
		{"timeline duplicate id", func() error {
			return r.RegisterTimeline(bg, Timeline{ID: 2, Parent: 1, Fork: 11})
		}, ErrInvalidFork},
		{"timeline bad id", func() error {
			return r.RegisterTimeline(bg, Timeline{ID: 1, Parent: 0, Fork: 0})
		}, ErrInvalidFork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.fn(), tc.want) {
				t.Fatalf("%s: err = %v, want %v", tc.name, tc.fn(), tc.want)
			}
		})
	}
}
