package pitr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// effectiveAt models "running directly on timeline target to position p":
// it returns the timeline whose own log produced position p.
func effectiveAt(store *Store, target TimelineID, p Position) TimelineID {
	tl := store.timelines[target]
	for tl.ID != InitialTimeline && p < tl.Fork {
		tl = store.timelines[tl.Parent]
	}
	return tl.ID
}

// assertEquivalent replays the plan and checks every replayed position is
// attributed to exactly the timeline that a direct run on the target would
// use, with no missing or duplicated positions.
func assertEquivalent(t *testing.T, store *Store, plan Plan) {
	t.Helper()
	cursor := plan.Backup.End
	for _, step := range plan.Steps {
		if step.Start != cursor {
			t.Fatalf("steps not contiguous: cursor=%d step=%+v", cursor, step)
		}
		for p := step.Start; p < step.End; p++ {
			if got := effectiveAt(store, plan.TargetTimeline, p); got != step.Timeline {
				t.Fatalf("position %d replayed from timeline %d, direct run would use %d", p, step.Timeline, got)
			}
		}
		cursor = step.End
	}
	if cursor != plan.ReplayEnd {
		t.Fatalf("replay end=%d, want %d", cursor, plan.ReplayEnd)
	}
}

// buildThreeLevelStore builds timelines 1 -> 2@100 -> 3@250 with segments
// timeline1 [0,120), timeline2 [100,260), timeline3 [250,400) and backups
// (1,50), (2,150), (3,300).
func buildThreeLevelStore(t *testing.T, logger io.Writer) *Store {
	t.Helper()
	ctx := context.Background()
	store := NewStore(logger)
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 100}))
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 3, Parent: 2, Fork: 250}))
	for _, seg := range []Segment{{1, 0, 120}, {2, 100, 260}, {3, 250, 400}} {
		mustOK(t, store.AddSegment(ctx, seg))
	}
	for _, b := range []Backup{{1, 50}, {2, 150}, {3, 300}} {
		mustOK(t, store.AddBackup(ctx, b))
	}
	return store
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestThreeLevelPlansAndEquivalence(t *testing.T) {
	var log bytes.Buffer
	ctx := context.Background()
	store := buildThreeLevelStore(t, &log)

	// A store whose only backup is (1,50): replay must span all three
	// regions, proving positions are taken from the effective timeline.
	var spanLog bytes.Buffer
	spanStore := buildThreeLevelStore(t, &spanLog)
	spanStore.backups = map[TimelineID][]Backup{
		1: {{1, 50}},
		2: nil,
		3: nil,
	}
	spanPlan, err := spanStore.Plan(ctx, 3, 349, IncludeTarget)
	mustOK(t, err)
	wantSteps := []PlanStep{{1, 50, 100}, {2, 100, 250}, {3, 250, 350}}
	if !reflect.DeepEqual(spanPlan.Steps, wantSteps) {
		t.Fatalf("steps=%+v, want %+v", spanPlan.Steps, wantSteps)
	}
	if spanPlan.Backup != (Backup{1, 50}) || spanPlan.ReplayEnd != 350 {
		t.Fatalf("plan=%+v", spanPlan)
	}
	assertEquivalent(t, spanStore, spanPlan)

	// With all backups present the greatest bound (3,300) is chosen.
	plan, err := store.Plan(ctx, 3, 349, IncludeTarget)
	mustOK(t, err)
	if plan.Backup != (Backup{3, 300}) || plan.ReplayEnd != 350 {
		t.Fatalf("plan=%+v", plan)
	}
	if !reflect.DeepEqual(plan.Steps, []PlanStep{{3, 300, 350}}) {
		t.Fatalf("steps=%+v", plan.Steps)
	}
	assertEquivalent(t, store, plan)

	plan2, err := store.Plan(ctx, 3, 130, IncludeTarget)
	mustOK(t, err)
	if plan2.Backup != (Backup{1, 50}) {
		t.Fatalf("backup=%+v", plan2.Backup)
	}
	if !reflect.DeepEqual(plan2.Steps, []PlanStep{{1, 50, 100}, {2, 100, 131}}) {
		t.Fatalf("steps=%+v", plan2.Steps)
	}
	assertEquivalent(t, store, plan2)

	plan3, err := store.Plan(ctx, 2, 200, ExcludeTarget)
	mustOK(t, err)
	if plan3.Backup != (Backup{2, 150}) || plan3.ReplayEnd != 200 {
		t.Fatalf("plan3=%+v", plan3)
	}
	if !reflect.DeepEqual(plan3.Steps, []PlanStep{{2, 150, 200}}) {
		t.Fatalf("steps=%+v", plan3.Steps)
	}
	assertEquivalent(t, store, plan3)

	if !bytes.Contains(log.Bytes(), []byte("输入 Plan")) ||
		!bytes.Contains(log.Bytes(), []byte("输出 Plan: 成功")) ||
		!bytes.Contains(log.Bytes(), []byte("判定依据")) {
		t.Fatalf("log missing input/output/reason:\n%s", log.String())
	}
}

func TestForkInsideSegmentOldSegmentsUnusable(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 100}))
	mustOK(t, store.AddSegment(ctx, Segment{1, 0, 200}))
	mustOK(t, store.AddBackup(ctx, Backup{1, 50}))

	_, err := store.Plan(ctx, 2, 150, IncludeTarget)
	if !errors.Is(err, ErrLogGap) || err.Error() != "pitr: gap in archived log at position 100" {
		t.Fatalf("err=%v, want gap at 100", err)
	}

	plan, err := store.Plan(ctx, 2, 99, IncludeTarget)
	mustOK(t, err)
	if !reflect.DeepEqual(plan.Steps, []PlanStep{{1, 50, 100}}) {
		t.Fatalf("steps=%+v", plan.Steps)
	}
	assertEquivalent(t, store, plan)

	mustOK(t, store.AddSegment(ctx, Segment{2, 100, 180}))
	plan2, err := store.Plan(ctx, 2, 160, IncludeTarget)
	mustOK(t, err)
	if !reflect.DeepEqual(plan2.Steps, []PlanStep{{1, 50, 100}, {2, 100, 161}}) {
		t.Fatalf("steps=%+v", plan2.Steps)
	}
	assertEquivalent(t, store, plan2)
}

func TestTargetModeBoundaries(t *testing.T) {
	ctx := context.Background()
	store := buildThreeLevelStore(t, nil)

	plan, err := store.Plan(ctx, 2, 150, ExcludeTarget)
	mustOK(t, err)
	if plan.ReplayEnd != 150 || len(plan.Steps) != 0 || plan.Backup.End != 150 {
		t.Fatalf("plan=%+v", plan)
	}

	plan2, err := store.Plan(ctx, 2, 150, IncludeTarget)
	mustOK(t, err)
	if plan2.ReplayEnd != 151 || !reflect.DeepEqual(plan2.Steps, []PlanStep{{2, 150, 151}}) {
		t.Fatalf("plan2=%+v", plan2)
	}
	assertEquivalent(t, store, plan2)

	plan3, err := store.Plan(ctx, 3, 250, ExcludeTarget)
	mustOK(t, err)
	if !reflect.DeepEqual(plan3.Steps, []PlanStep{{2, 150, 250}}) {
		t.Fatalf("plan3=%+v", plan3.Steps)
	}
	plan4, err := store.Plan(ctx, 3, 250, IncludeTarget)
	mustOK(t, err)
	if !reflect.DeepEqual(plan4.Steps, []PlanStep{{2, 150, 250}, {3, 250, 251}}) {
		t.Fatalf("plan4=%+v", plan4.Steps)
	}
	assertEquivalent(t, store, plan3)
	assertEquivalent(t, store, plan4)
}

func TestBackupSelectionAndTies(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 100}))
	mustOK(t, store.AddSegment(ctx, Segment{1, 0, 200}))
	mustOK(t, store.AddSegment(ctx, Segment{2, 100, 200}))

	mustOK(t, store.AddBackup(ctx, Backup{1, 100}))
	mustOK(t, store.AddBackup(ctx, Backup{2, 100}))
	plan, err := store.Plan(ctx, 2, 150, IncludeTarget)
	mustOK(t, err)
	if plan.Backup != (Backup{2, 100}) {
		t.Fatalf("backup=%+v, want timeline 2", plan.Backup)
	}
	assertEquivalent(t, store, plan)

	// Parent bound 140 is past the replay window end 151? no -- but it is
	// within the fork (100) side? 140 >= fork(2)=100, yet (1,140) covers
	// positions 100..140 that timeline 1 does not own on this target.
	// It is therefore not a usable backup (bounds must sit in the backup's
	// own effective interval, closed at the fork only).
	mustOK(t, store.AddBackup(ctx, Backup{1, 140}))
	plan2, err := store.Plan(ctx, 2, 150, IncludeTarget)
	mustOK(t, err)
	if plan2.Backup != (Backup{2, 100}) {
		t.Fatalf("backup=%+v, want (2,100) (parent bound 140 is beyond its own fork)", plan2.Backup)
	}

	// Greatest bound first; ties broken by timeline id. Build a clean
	// scenario where the bound 80 is available on both 1 and 2.
	t2 := NewStore(nil)
	mustOK(t, t2.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 80}))
	mustOK(t, t2.AddSegment(ctx, Segment{1, 0, 160}))
	mustOK(t, t2.AddSegment(ctx, Segment{2, 80, 160}))
	mustOK(t, t2.AddBackup(ctx, Backup{1, 50}))
	mustOK(t, t2.AddBackup(ctx, Backup{1, 80}))
	mustOK(t, t2.AddBackup(ctx, Backup{2, 80}))
	planT, err := t2.Plan(ctx, 2, 150, IncludeTarget)
	mustOK(t, err)
	if planT.Backup != (Backup{2, 80}) {
		t.Fatalf("backup=%+v, want tie winner (2,80)", planT.Backup)
	}
	assertEquivalent(t, t2, planT)

	// A backup past the replay end is unusable: for end 91 only a backup
	// with bound <= 91 on an effective timeline qualifies.
	mustOK(t, store.AddBackup(ctx, Backup{1, 90}))
	plan3, err := store.Plan(ctx, 2, 90, IncludeTarget)
	mustOK(t, err)
	if plan3.Backup != (Backup{1, 90}) {
		t.Fatalf("backup=%+v, want (1,90)", plan3.Backup)
	}
	assertEquivalent(t, store, plan3)
}

func TestGapLocation(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	mustOK(t, store.AddSegment(ctx, Segment{1, 0, 40}))
	mustOK(t, store.AddSegment(ctx, Segment{1, 60, 100}))
	mustOK(t, store.AddBackup(ctx, Backup{1, 10}))

	_, err := store.Plan(ctx, 1, 90, IncludeTarget)
	if err == nil || !errors.Is(err, ErrLogGap) || err.Error() != "pitr: gap in archived log at position 40" {
		t.Fatalf("err=%v, want gap at 40", err)
	}

	plan, err := store.Plan(ctx, 1, 39, IncludeTarget)
	mustOK(t, err)
	assertEquivalent(t, store, plan)
}

func TestErrorPrecedence(t *testing.T) {
	ctx := context.Background()

	store := NewStore(nil)
	if _, err := store.Plan(ctx, 9, 10, ExcludeTarget); !errors.Is(err, ErrTimelineNotFound) {
		t.Fatalf("err=%v", err)
	}

	store = NewStore(nil)
	if _, err := store.Plan(ctx, 1, 500, ExcludeTarget); !errors.Is(err, ErrBeyondArchive) {
		t.Fatalf("err=%v", err)
	}

	store = NewStore(nil)
	mustOK(t, store.AddSegment(ctx, Segment{1, 0, 100}))
	if _, err := store.Plan(ctx, 1, 50, ExcludeTarget); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("err=%v", err)
	}

	// 4. Gap inside the replay window while the archived end is far
	// enough: segment [0,100) plus [120,200), backup bound 100, target
	// inside the window -> first gap at 100.
	mustOK(t, store.AddSegment(ctx, Segment{1, 120, 200}))
	mustOK(t, store.AddBackup(ctx, Backup{1, 100}))
	_, err := store.Plan(ctx, 1, 150, ExcludeTarget)
	if !errors.Is(err, ErrLogGap) || err.Error() != "pitr: gap in archived log at position 100" {
		t.Fatalf("err=%v, want gap at 100", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 100}))

	if err := store.RegisterTimeline(ctx, Timeline{ID: 2, Parent: 1, Fork: 200}); !errors.Is(err, ErrTimelineExists) {
		t.Fatalf("dup: %v", err)
	}
	if err := store.RegisterTimeline(ctx, Timeline{ID: 5, Parent: 4, Fork: 10}); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	if err := store.RegisterTimeline(ctx, Timeline{ID: 6, Parent: 1, Fork: -1}); !errors.Is(err, ErrForkBeforeParentFork) {
		t.Fatalf("earlier fork rule: %v", err)
	}
	if err := store.RegisterTimeline(ctx, Timeline{ID: 7, Parent: 2, Fork: 99}); !errors.Is(err, ErrForkBeforeParentFork) {
		t.Fatalf("nested earlier fork rule: %v", err)
	}
	// Equal fork is legal (sibling-style degenerate timeline).
	mustOK(t, store.RegisterTimeline(ctx, Timeline{ID: 8, Parent: 2, Fork: 100}))
	if err := store.AddSegment(ctx, Segment{9, 0, 10}); !errors.Is(err, ErrTimelineNotFound) {
		t.Fatalf("segment on unknown timeline: %v", err)
	}
	if err := store.AddSegment(ctx, Segment{1, 10, 10}); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("empty segment: %v", err)
	}
}

func TestRecoverNewTimelineAndForkPoint(t *testing.T) {
	ctx := context.Background()
	store := buildThreeLevelStore(t, nil)

	res, err := store.Recover(ctx, 3, 300, ExcludeTarget)
	mustOK(t, err)
	if res.NewTimeline != (Timeline{ID: 4, Parent: 3, Fork: 300}) {
		t.Fatalf("new timeline=%+v", res.NewTimeline)
	}
	assertEquivalent(t, store, res.Plan)

	// The new timeline has no own segments; its effective history still
	// flows from the parent chain. The greatest usable backup for end 300
	// is (3,300); the empty replay succeeds.
	plan, err := store.Plan(ctx, 4, 300, ExcludeTarget)
	mustOK(t, err)
	if plan.Backup != (Backup{3, 300}) || len(plan.Steps) != 0 {
		t.Fatalf("plan on new timeline=%+v", plan)
	}

	res2, err := store.Recover(ctx, 3, 300, IncludeTarget)
	mustOK(t, err)
	if res2.NewTimeline != (Timeline{ID: 5, Parent: 3, Fork: 301}) {
		t.Fatalf("new timeline=%+v", res2.NewTimeline)
	}
	assertEquivalent(t, store, res2.Plan)
}

func TestRecoverRejectionChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	mustOK(t, store.AddSegment(ctx, Segment{1, 0, 50}))
	mustOK(t, store.AddSegment(ctx, Segment{1, 60, 100}))
	mustOK(t, store.AddBackup(ctx, Backup{1, 40}))

	before := len(store.timelines)
	// Gap inside the replay window [40,80): first gap at 50.
	if _, err := store.Recover(ctx, 1, 80, ExcludeTarget); !errors.Is(err, ErrLogGap) {
		t.Fatalf("err=%v", err)
	}
	if len(store.timelines) != before {
		t.Fatalf("registry changed on rejected (gap) recovery")
	}
	if _, err := store.Recover(ctx, 99, 10, ExcludeTarget); !errors.Is(err, ErrTimelineNotFound) {
		t.Fatalf("err=%v", err)
	}
	if len(store.timelines) != before {
		t.Fatalf("registry changed on unknown-target recovery")
	}

	mustOK(t, store.AddSegment(ctx, Segment{1, 50, 60}))
	res, err := store.Recover(ctx, 1, 50, IncludeTarget)
	mustOK(t, err)
	if res.NewTimeline.ID != 2 {
		t.Fatalf("id=%d, want 2", res.NewTimeline.ID)
	}
}

func TestConcurrentRecoverUniqueConsecutiveIDs(t *testing.T) {
	ctx := context.Background()
	store := buildThreeLevelStore(t, nil)

	const n = 40
	var wg sync.WaitGroup
	ids := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := store.Recover(ctx, 3, Position(350+i%10), IncludeTarget)
			if err != nil {
				t.Errorf("recover %d: %v", i, err)
				return
			}
			ids[i] = res.NewTimeline.ID
		}(i)
	}
	close(start)
	wg.Wait()

	sort.Ints(ids)
	for i, id := range ids {
		if id != 4+i {
			t.Fatalf("ids=%v, want consecutive 4..%d", ids, 3+n)
		}
	}
}

func TestConcurrentArchivePlanRecover(t *testing.T) {
	ctx := context.Background()
	store := buildThreeLevelStore(t, nil)

	var wg sync.WaitGroup
	start := make(chan struct{})
	done := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			<-start
			for j := 0; ; j++ {
				select {
				case <-done:
					return
				default:
				}
				start := Position(400 + (base+j)*10)
				_ = store.AddSegment(ctx, Segment{Timeline: 1, Start: start, End: start + 5})
			}
		}(i * 1000)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for {
			select {
			case <-done:
				return
			default:
			}
			_, _ = store.Plan(ctx, 3, 320, IncludeTarget)
		}
	}()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = store.Recover(ctx, 3, 310, ExcludeTarget)
		}()
	}
	close(start)
	for i := 0; i < 10; i++ {
		_, _ = store.Recover(ctx, 3, 310, ExcludeTarget)
	}
	close(done)
	wg.Wait()
}

func TestPlanDeterminism(t *testing.T) {
	ctx := context.Background()
	store := buildThreeLevelStore(t, nil)
	// Add overlapping and duplicate segments; plans must stay identical.
	mustOK(t, store.AddSegment(ctx, Segment{1, 20, 80}))
	mustOK(t, store.AddSegment(ctx, Segment{2, 100, 200}))
	mustOK(t, store.AddSegment(ctx, Segment{2, 100, 260}))
	mustOK(t, store.AddBackup(ctx, Backup{1, 50}))

	first, err := store.Plan(ctx, 3, 349, IncludeTarget)
	mustOK(t, err)
	for i := 0; i < 10; i++ {
		next, err := store.Plan(ctx, 3, 349, IncludeTarget)
		mustOK(t, err)
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("plan mismatch:\n%+v\n%+v", first, next)
		}
	}
}
