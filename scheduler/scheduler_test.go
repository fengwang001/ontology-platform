package scheduler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func newTestScheduler(t *testing.T, n int64, logW io.Writer) *Scheduler {
	t.Helper()
	s, err := New(n, 0, logW)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewValidation(t *testing.T) {
	for _, n := range []int64{0, -1, -10} {
		if _, err := New(n, 0, nil); !errors.Is(err, ErrInvalidNodes) {
			t.Fatalf("New(%d) err=%v want ErrInvalidNodes", n, err)
		}
	}
}

func TestSubmitValidationNoMutation(t *testing.T) {
	var buf bytes.Buffer
	s := newTestScheduler(t, 4, &buf)

	for _, j := range []Job{
		{ID: "bad-zero", Nodes: 0, Duration: 1},
		{ID: "bad-neg", Nodes: -2, Duration: 1},
		{ID: "bad-big", Nodes: 5, Duration: 1},
		{ID: "bad-dur", Nodes: 1, Duration: 0},
		{ID: "bad-dur2", Nodes: 1, Duration: -3},
	} {
		s.Submit(j)
	}
	s.Submit(Job{ID: "dup", Nodes: 4, Duration: 5})
	s.Submit(Job{ID: "dup", Nodes: 1, Duration: 5}) // 运行中重复
	s.Submit(Job{ID: "queued", Nodes: 4, Duration: 5})
	s.Submit(Job{ID: "queued", Nodes: 1, Duration: 5}) // 队列中重复

	after := s.Query()
	if len(after.Running) != 1 || after.Running[0].ID != "dup" {
		t.Fatalf("invalid submits changed running set: %+v", after.Running)
	}
	if len(after.Queue) != 1 || after.Queue[0].ID != "queued" {
		t.Fatalf("invalid submits changed queue: %+v", after.Queue)
	}
}

func TestFinishAndClockValidation(t *testing.T) {
	s := newTestScheduler(t, 4, nil)
	s.Submit(Job{ID: "a", Nodes: 2, Duration: 10})

	if err := s.Finish("ghost", 0); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("Finish missing job err=%v", err)
	}
	if _, err := s.Advance(5); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := s.Finish("a", 3); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Finish backdated err=%v", err)
	}
	if _, err := s.Advance(2); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Advance rewind err=%v", err)
	}

	before := s.Query()
	s.Finish("ghost", 5)
	after := s.Query()
	if len(before.Running) != len(after.Running) || after.Running[0].ID != "a" {
		t.Fatalf("rejected finish mutated running set: %+v -> %+v", before, after)
	}

	if err := s.Finish("a", 7); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := s.Query(); len(got.Running) != 0 {
		t.Fatalf("job still running after finish: %+v", got.Running)
	}
}

func TestImmediateHeadLaunch(t *testing.T) {
	s := newTestScheduler(t, 10, nil)
	s.Submit(Job{ID: "a", Nodes: 3, Duration: 4})
	s.Submit(Job{ID: "b", Nodes: 5, Duration: 2})
	s.Submit(Job{ID: "c", Nodes: 3, Duration: 1})
	snap := s.Query()
	if len(snap.Running) != 2 || snap.Running[0].ID != "a" || snap.Running[1].ID != "b" {
		t.Fatalf("want a,b running, got %+v", snap.Running)
	}
	if len(snap.Queue) != 1 || snap.Queue[0].ID != "c" {
		t.Fatalf("want c queued, got %+v", snap.Queue)
	}
}

func TestBackfillEndsExactlyAtShadow(t *testing.T) {
	var buf bytes.Buffer
	s := newTestScheduler(t, 10, &buf)
	s.Submit(Job{ID: "A", Nodes: 8, Duration: 10})
	s.Submit(Job{ID: "B", Nodes: 8, Duration: 6})
	s.Submit(Job{ID: "C", Nodes: 2, Duration: 10}) // now+dur == shadow，边界回填

	snap := s.Query()
	if len(snap.Running) != 2 || snap.Running[0].ID != "A" || snap.Running[1].ID != "C" {
		t.Fatalf("want A,C running, got %+v", snap.Running)
	}
	if len(snap.Queue) != 1 || snap.Queue[0].ID != "B" || snap.Queue[0].Shadow != 10 {
		t.Fatalf("want B reserved with shadow=10, got %+v", snap.Queue)
	}

	if _, err := s.Advance(10); err != nil {
		t.Fatal(err)
	}
	snap = s.Query()
	if len(snap.Running) != 1 || snap.Running[0].ID != "B" || snap.Running[0].StartTime != 10 {
		t.Fatalf("B must start exactly at shadow 10, got %+v", snap.Running)
	}
	log := buf.String()
	for _, want := range []string{"reserve", "backfill-short", "forced-end", "input", "output"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestMultipleBackfillsShareSurplus(t *testing.T) {
	s := newTestScheduler(t, 10, nil)
	s.Submit(Job{ID: "A", Nodes: 6, Duration: 10})
	s.Submit(Job{ID: "B", Nodes: 6, Duration: 3}) // shadow=10 surplus=4
	s.Submit(Job{ID: "C", Nodes: 3, Duration: 20})
	s.Submit(Job{ID: "D", Nodes: 1, Duration: 20})
	s.Submit(Job{ID: "E", Nodes: 2, Duration: 20}) // 富余耗尽，放不下

	snap := s.Query()
	ids := map[string]bool{}
	used := int64(0)
	for _, r := range snap.Running {
		ids[r.ID] = true
		used += r.Nodes
	}
	if !ids["A"] || !ids["C"] || !ids["D"] || ids["B"] || ids["E"] {
		t.Fatalf("want exactly A,C,D running, got %+v", snap.Running)
	}
	if used > 10 {
		t.Fatalf("nodes oversubscribed: used=%d", used)
	}
	if len(snap.Queue) != 2 || snap.Queue[0].ID != "B" || snap.Queue[1].ID != "E" {
		t.Fatalf("want B,E queued, got %+v", snap.Queue)
	}

	s.Advance(10)
	snap = s.Query()
	headRunning := false
	used = 0
	for _, r := range snap.Running {
		if r.ID == "B" {
			headRunning = r.StartTime == 10
		}
		used += r.Nodes
	}
	if !headRunning {
		t.Fatalf("reserved head B must start no later than shadow 10: %+v", snap.Running)
	}
	if used > 10 {
		t.Fatalf("nodes oversubscribed at t=10: used=%d", used)
	}
}

func TestEarlyFinishRecomputesReservation(t *testing.T) {
	s := newTestScheduler(t, 10, nil)
	s.Submit(Job{ID: "A", Nodes: 8, Duration: 10})
	s.Submit(Job{ID: "B", Nodes: 8, Duration: 4})
	if q := s.Query().Queue; q[0].Shadow != 10 {
		t.Fatalf("initial shadow=%d want 10", q[0].Shadow)
	}
	if err := s.Finish("A", 5); err != nil {
		t.Fatal(err)
	}
	snap := s.Query()
	if len(snap.Running) != 1 || snap.Running[0].ID != "B" || snap.Running[0].StartTime != 5 {
		t.Fatalf("B should start at 5 after early finish, got %+v", snap.Running)
	}
}

func TestForcedEndAtDuration(t *testing.T) {
	s := newTestScheduler(t, 4, nil)
	s.Submit(Job{ID: "a", Nodes: 4, Duration: 3})
	s.Advance(2)
	if got := s.Query(); len(got.Running) != 1 {
		t.Fatalf("job should still run at t=2: %+v", got.Running)
	}
	s.Advance(3)
	if got := s.Query(); len(got.Running) != 0 {
		t.Fatalf("job must be force-ended at t=3: %+v", got.Running)
	}
}

// TestRandomLoadReservationUpperBound：随机提交/提前结束/逐拍推进，
// 恒断言节点不超分，且每个作业的实际启动时刻不晚于它成为队首时的影子时刻。
func TestRandomLoadReservationUpperBound(t *testing.T) {
	const N = int64(8)
	const steps = 500
	rng := rand.New(rand.NewSource(20260930))

	s, err := New(N, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	firstShadow := map[string]int64{}
	startAt := map[string]int64{}
	submitted := map[string]bool{}
	wasHead := map[string]bool{}

	for step := 0; step < steps; step++ {
		now := int64(step)
		s.Advance(now)

		switch rng.Intn(3) {
		case 0:
			id := fmt.Sprintf("j%d", rng.Intn(80))
			if !submitted[id] {
				s.Submit(Job{
					ID:       id,
					Nodes:    int64(1 + rng.Intn(int(N))),
					Duration: int64(1 + rng.Intn(15)),
				})
				submitted[id] = true
			}
		case 1:
			snap := s.Query()
			if len(snap.Running) > 0 {
				victim := snap.Running[rng.Intn(len(snap.Running))]
				if err := s.Finish(victim.ID, now); err != nil &&
					!errors.Is(err, ErrJobNotRunning) {
					t.Fatal(err)
				}
			}
		}

		snap := s.Query()
		used := int64(0)
		for _, r := range snap.Running {
			used += r.Nodes
			if _, ok := startAt[r.ID]; !ok {
				startAt[r.ID] = r.StartTime
			}
		}
		if used > N {
			t.Fatalf("oversubscription at t=%d used=%d", now, used)
		}
		if len(snap.Queue) > 0 {
			head := snap.Queue[0]
			if !wasHead[head.ID] {
				firstShadow[head.ID] = head.Shadow
				wasHead[head.ID] = true
			}
		}
	}

	for id, shadow := range firstShadow {
		start, ok := startAt[id]
		if !ok {
			continue // 窗口结束仍在队列中的作业，尚无实际启动时刻
		}
		if start > shadow {
			t.Fatalf("job %s started at %d later than its reservation shadow %d", id, start, shadow)
		}
	}
}

// TestDeterministicReplay：相同的提交/结束/时钟序列两次重放，启动序列完全一致。
func TestDeterministicReplay(t *testing.T) {
	const N = int64(12)
	build := func(seed int64) ([]string, []Snapshot) {
		rng := rand.New(rand.NewSource(seed))
		s, _ := New(N, 0, nil)
		var launchSeq []string
		seen := map[string]bool{}
		var snaps []Snapshot
		submitted := map[string]bool{}
		for step := 0; step < 300; step++ {
			now := int64(step)
			s.Advance(now)
			if rng.Intn(2) == 0 {
				id := fmt.Sprintf("job%d", rng.Intn(50))
				if !submitted[id] {
					s.Submit(Job{ID: id, Nodes: int64(1 + rng.Intn(int(N))), Duration: int64(1 + rng.Intn(12))})
					submitted[id] = true
				}
			} else {
				snap := s.Query()
				if len(snap.Running) > 0 && rng.Intn(2) == 0 {
					s.Finish(snap.Running[rng.Intn(len(snap.Running))].ID, now)
				}
			}
			snap := s.Query()
			snaps = append(snaps, snap)
			running := map[string]RunningJob{}
			for _, r := range snap.Running {
				running[r.ID] = r
			}
			// 以“时刻:id”记录启动顺序。
			order := make([]string, 0, len(running))
			for id := range running {
				order = append(order, id)
			}
			sort.Strings(order)
			for _, id := range order {
				key := fmt.Sprintf("%d:%s", running[id].StartTime, id)
				if !seen[key] {
					seen[key] = true
					launchSeq = append(launchSeq, key)
				}
			}
		}
		return launchSeq, snaps
	}

	seq1, snaps1 := build(42)
	seq2, snaps2 := build(42)
	if strings.Join(seq1, ",") != strings.Join(seq2, ",") {
		t.Fatalf("launch sequences differ:\n%v\n%v", seq1, seq2)
	}
	if len(snaps1) != len(snaps2) {
		t.Fatalf("snapshot count differs: %d vs %d", len(snaps1), len(snaps2))
	}
	for i := range snaps1 {
		a, b := snaps1[i], snaps2[i]
		if a.Now != b.Now || len(a.Running) != len(b.Running) || len(a.Queue) != len(b.Queue) {
			t.Fatalf("snapshot %d differs:\n%+v\n%+v", i, a, b)
		}
		for j := range a.Running {
			if a.Running[j] != b.Running[j] {
				t.Fatalf("running %d at snapshot %d differs: %+v vs %+v", j, i, a.Running[j], b.Running[j])
			}
		}
		for j := range a.Queue {
			if a.Queue[j] != b.Queue[j] {
				t.Fatalf("queue %d at snapshot %d differs: %+v vs %+v", j, i, a.Queue[j], b.Queue[j])
			}
		}
	}
}

// TestConcurrentCalls：提交/结束/查询/时钟并发调用，竞态检测下不超分、不崩溃。
func TestConcurrentCalls(t *testing.T) {
	s, err := New(6, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w + 1)))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				id := fmt.Sprintf("w%d-j%d", w, rng.Intn(20))
				s.Submit(Job{ID: id, Nodes: int64(1 + rng.Intn(6)), Duration: int64(1 + rng.Intn(5))})
				snap := s.Query()
				used := int64(0)
				for _, r := range snap.Running {
					used += r.Nodes
				}
				if used > 6 {
					t.Errorf("oversubscription used=%d", used)
					return
				}
				if len(snap.Running) > 0 && rng.Intn(2) == 0 {
					s.Finish(snap.Running[rng.Intn(len(snap.Running))].ID, snap.Now)
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for t := int64(1); t <= 60; t++ {
			s.Advance(t)
		}
		close(stop)
	}()

	wg.Wait()
	snap := s.Query()
	used := int64(0)
	for _, r := range snap.Running {
		used += r.Nodes
	}
	if used > 6 {
		t.Fatalf("final oversubscription used=%d", used)
	}
}
