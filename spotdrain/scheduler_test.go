package spotdrain

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, g, pod, bud int64) *Scheduler {
	t.Helper()
	s, err := NewScheduler(g, pod, bud)
	if err != nil {
		t.Fatalf("NewScheduler(%d,%d,%d): %v", g, pod, bud, err)
	}
	return s
}

func mustAddNode(t *testing.T, s *Scheduler, id int64, kind NodeKind, slots int) {
	t.Helper()
	if err := s.AddNode(id, kind, slots); err != nil {
		t.Fatalf("AddNode(%d): %v", id, err)
	}
}

func mustAddTask(t *testing.T, s *Scheduler, id, prio, w, iv, ck int64) {
	t.Helper()
	if err := s.AddTask(id, prio, w, iv, ck); err != nil {
		t.Fatalf("AddTask(%d): %v", id, err)
	}
}

func mustReport(t *testing.T, s *Scheduler, id, p int64) {
	t.Helper()
	if err := s.Report(id, p); err != nil {
		t.Fatalf("Report(%d,%d): %v", id, p, err)
	}
}

func taskState(t *testing.T, s *Scheduler, id int64) TaskInfo {
	t.Helper()
	info, ok := s.TaskInfo(id)
	if !ok {
		t.Fatalf("TaskInfo(%d): not found", id)
	}
	return info
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 100, 2, 600)
	mustAddNode(t, s, 1, Spot, 5)
	mustAddTask(t, s, 1, 0, 500, 100, 40)
	mustAddTask(t, s, 2, 0, 400, 50, 45)
	mustAddTask(t, s, 3, 0, 300, 30, 20)
	mustAddTask(t, s, 4, 0, 900, 200, 30)
	mustAddTask(t, s, 5, 0, 250, 50, 15)
	s.Place()
	for id := int64(1); id <= 5; id++ {
		if info := taskState(t, s, id); info.State != Running || info.NodeID != 1 {
			t.Fatalf("task %d: got %+v, want running on node 1", id, info)
		}
	}
	mustReport(t, s, 1, 330)
	mustReport(t, s, 2, 240)
	mustReport(t, s, 3, 250)
	mustReport(t, s, 4, 700)
	mustReport(t, s, 5, 100)
	wantCp := map[int64]int64{1: 300, 2: 200, 3: 240, 4: 600, 5: 100}
	for id, cp := range wantCp {
		if info := taskState(t, s, id); info.Cp != cp {
			t.Fatalf("task %d: cp=%d, want %d", id, info.Cp, cp)
		}
	}

	completed, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if !reflect.DeepEqual(completed, []int64{3}) {
		t.Fatalf("completed=%v, want [3]", completed)
	}
	if !reflect.DeepEqual(saved, []int64{2, 4, 5}) {
		t.Fatalf("saved=%v, want [2 4 5]", saved)
	}
	if !reflect.DeepEqual(dropped, []int64{1}) {
		t.Fatalf("dropped=%v, want [1]", dropped)
	}
	if info := taskState(t, s, 2); info.Cp != 240 {
		t.Fatalf("task 2 cp=%d, want 240 (checkpointed at notice)", info.Cp)
	}
	if info := taskState(t, s, 4); info.Cp != 700 {
		t.Fatalf("task 4 cp=%d, want 700 (checkpointed at notice)", info.Cp)
	}

	mustReport(t, s, 3, 300)
	if info := taskState(t, s, 3); info.State != Completed {
		t.Fatalf("task 3 state=%v, want Completed", info.State)
	}

	rework, err := s.Expire(1, 100)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	wantRework := map[int64]int64{1: 30, 2: 0, 4: 0, 5: 0}
	if !reflect.DeepEqual(rework, wantRework) {
		t.Fatalf("rework=%v, want %v", rework, wantRework)
	}
	wantP := map[int64]int64{1: 300, 2: 240, 4: 700, 5: 100}
	for id, p := range wantP {
		info := taskState(t, s, id)
		if info.State != Pending || info.P != p || info.Rs != 1 {
			t.Fatalf("task %d: %+v, want pending p=%d rs=1", id, info, p)
		}
	}
	if got := s.TotalRework(); got != 30 {
		t.Fatalf("TotalRework=%d, want 30", got)
	}

	mustAddNode(t, s, 2, Spot, 2)
	mustAddNode(t, s, 3, OnDemand, 5)
	s.Place()
	if info := taskState(t, s, 1); info.State != Running || info.NodeID != 2 {
		t.Fatalf("task 1: %+v, want running on node 2", info)
	}
	if info := taskState(t, s, 2); info.State != Running || info.NodeID != 2 {
		t.Fatalf("task 2: %+v, want running on node 2", info)
	}
	if info := taskState(t, s, 4); info.State != Running || info.NodeID != 3 {
		t.Fatalf("task 4: %+v, want running on node 3", info)
	}
	if got := s.Budget(); got != 200 {
		t.Fatalf("Budget=%d, want 200", got)
	}
	if info := taskState(t, s, 5); info.State != Pending {
		t.Fatalf("task 5: %+v, want pending (budget 300 > 200)", info)
	}
}

// TestSpecExampleCheckpointTie: with task 1's ck=25 the cumulative cost hits
// exactly G, so task 1 is saved instead of dropped.
func TestSpecExampleCheckpointTie(t *testing.T) {
	s := mustNew(t, 100, 2, 600)
	mustAddNode(t, s, 1, Spot, 5)
	mustAddTask(t, s, 1, 0, 500, 100, 25)
	mustAddTask(t, s, 2, 0, 400, 50, 45)
	mustAddTask(t, s, 3, 0, 300, 30, 20)
	mustAddTask(t, s, 4, 0, 900, 200, 30)
	mustAddTask(t, s, 5, 0, 250, 50, 15)
	s.Place()
	mustReport(t, s, 1, 330)
	mustReport(t, s, 2, 240)
	mustReport(t, s, 3, 250)
	mustReport(t, s, 4, 700)
	mustReport(t, s, 5, 100)
	_, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if !reflect.DeepEqual(saved, []int64{1, 2, 4, 5}) {
		t.Fatalf("saved=%v, want [1 2 4 5]", saved)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped=%v, want empty", dropped)
	}
}

// TestNaturalCompletionBoundary: remaining == G completes naturally;
// remaining == G+1 does not.
func TestNaturalCompletionBoundary(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddTask(t, s, 1, 0, 200, 10, 5) // remaining 200-100=100 == G
	mustAddTask(t, s, 2, 0, 201, 10, 5) // remaining 201-100=101 > G
	s.Place()
	mustReport(t, s, 1, 100)
	mustReport(t, s, 2, 100)
	completed, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if !reflect.DeepEqual(completed, []int64{1}) {
		t.Fatalf("completed=%v, want [1]", completed)
	}
	// Task 2: u = 100-100 = 0, saved without consuming time.
	if !reflect.DeepEqual(saved, []int64{2}) {
		t.Fatalf("saved=%v, want [2]", saved)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped=%v, want empty", dropped)
	}
}

// TestZeroUnsavedCostsNothing: u == 0 tasks are saved directly, occupy no
// order slot and consume no checkpoint budget.
func TestZeroUnsavedCostsNothing(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddTask(t, s, 1, 0, 1000, 10, 7) // u=0
	mustAddTask(t, s, 2, 0, 1000, 10, 7) // u=5, ck=7
	s.Place()
	mustReport(t, s, 1, 100) // cp=100, u=0
	mustReport(t, s, 2, 105) // cp=100, u=5
	_, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	// G=10: task 2 cumulative 7 <= 10 saved; u=0 task never competes.
	if !reflect.DeepEqual(saved, []int64{1, 2}) {
		t.Fatalf("saved=%v, want [1 2]", saved)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped=%v, want empty", dropped)
	}
}

// TestCumulativeBoundaryAndStop: cumulative == G saves, G+1 drops and stops
// every later task, even a smaller one.
func TestCumulativeBoundaryAndStop(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	mustAddNode(t, s, 1, Spot, 4)
	// u ordering: 1(50), 2(40), 3(30), 4(20); ck: 60, 40, 1, 1.
	// cumulative: 60, 100 (== G, saved), 101 (> G, dropped, stop), 4 dropped too.
	mustAddTask(t, s, 1, 0, 1000, 100, 60)
	mustAddTask(t, s, 2, 0, 1000, 100, 40)
	mustAddTask(t, s, 3, 0, 1000, 100, 1)
	mustAddTask(t, s, 4, 0, 1000, 100, 1)
	s.Place()
	mustReport(t, s, 1, 50) // cp=0, u=50
	mustReport(t, s, 2, 40) // cp=0, u=40
	mustReport(t, s, 3, 30) // cp=0, u=30
	mustReport(t, s, 4, 20) // cp=0, u=20
	completed, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if len(completed) != 0 {
		t.Fatalf("completed=%v, want empty", completed)
	}
	// cumulative 60, 100(==G saved); task 3 would make 101 > G: dropped and
	// stops the scan, so task 4 (ck=1, would fit alone) is dropped too.
	if !reflect.DeepEqual(saved, []int64{1, 2}) {
		t.Fatalf("saved=%v, want [1 2]", saved)
	}
	if !reflect.DeepEqual(dropped, []int64{3, 4}) {
		t.Fatalf("dropped=%v, want [3 4]", dropped)
	}
	if info := taskState(t, s, 1); info.Cp != 50 {
		t.Fatalf("task 1 cp=%d, want 50", info.Cp)
	}
	if info := taskState(t, s, 2); info.Cp != 40 {
		t.Fatalf("task 2 cp=%d, want 40", info.Cp)
	}
	if info := taskState(t, s, 3); info.Cp != 0 {
		t.Fatalf("task 3 cp=%d, want 0 (dropped keeps old cp)", info.Cp)
	}
}

// TestCheckpointFloorAndMonotonic: cp = floor(p'/iv)*iv and never decreases.
func TestCheckpointFloorAndMonotonic(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	mustAddNode(t, s, 1, Spot, 1)
	mustAddTask(t, s, 1, 0, 1000, 100, 5)
	s.Place()
	mustReport(t, s, 1, 250) // floor(250/100)*100 = 200
	if info := taskState(t, s, 1); info.Cp != 200 {
		t.Fatalf("cp=%d, want 200", info.Cp)
	}
	mustReport(t, s, 1, 260) // floor(260/100)*100 = 200, cp unchanged
	if info := taskState(t, s, 1); info.Cp != 200 {
		t.Fatalf("cp=%d, want 200 (monotonic)", info.Cp)
	}
	mustReport(t, s, 1, 399) // floor = 300
	if info := taskState(t, s, 1); info.Cp != 300 {
		t.Fatalf("cp=%d, want 300", info.Cp)
	}
}

// TestExpireRollbackRework: Expire rolls p back to cp, counts rework, and
// naturally-completing tasks that never reported completion also roll back.
func TestExpireRollbackRework(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	mustAddNode(t, s, 1, Spot, 3)
	mustAddTask(t, s, 1, 0, 1000, 100, 5)   // u=0 at notice -> saved
	mustAddTask(t, s, 2, 0, 1000, 100, 200) // dropped at notice (ck too big)
	mustAddTask(t, s, 3, 0, 150, 100, 5)    // remaining 50 <= G: natural completion
	s.Place()
	mustReport(t, s, 1, 250) // cp=200, u=50, saved -> cp=250
	mustReport(t, s, 2, 250) // cp=200, u=50
	mustReport(t, s, 3, 100) // cp=100
	completed, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if !reflect.DeepEqual(completed, []int64{3}) {
		t.Fatalf("completed=%v, want [3]", completed)
	}
	// u tie (50, 50): id 1 first, acc 5 <= 100 saved (cp=250);
	// id 2 acc 205 > 100 dropped (cp stays 200).
	if !reflect.DeepEqual(saved, []int64{1}) {
		t.Fatalf("saved=%v, want [1]", saved)
	}
	if !reflect.DeepEqual(dropped, []int64{2}) {
		t.Fatalf("dropped=%v, want [2]", dropped)
	}
	// Task 3 naturally completes but never reports; it still rolls back.
	rework, err := s.Expire(1, 100)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	want := map[int64]int64{1: 0, 2: 50, 3: 0}
	if !reflect.DeepEqual(rework, want) {
		t.Fatalf("rework=%v, want %v", rework, want)
	}
	wantP := map[int64]int64{1: 250, 2: 200, 3: 100}
	for id, p := range wantP {
		info := taskState(t, s, id)
		if info.State != Pending || info.P != p || info.Cp != p || info.Rs != 1 {
			t.Fatalf("task %d: %+v, want pending p=cp=%d rs=1", id, info, p)
		}
	}
	if got := s.TotalRework(); got != 50 {
		t.Fatalf("TotalRework=%d, want 50", got)
	}
	if _, ok := s.NodeInfo(1); ok {
		t.Fatal("node 1 should be removed after Expire")
	}
}

// TestSpotCandidateMostFreeSlots: pick the Spot node with the most free
// slots, ties broken by smaller id.
func TestSpotCandidateMostFreeSlots(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddNode(t, s, 2, Spot, 5)
	mustAddNode(t, s, 3, Spot, 5)
	mustAddTask(t, s, 1, 0, 100, 10, 1)
	mustAddTask(t, s, 2, 0, 100, 10, 1)
	mustAddTask(t, s, 3, 0, 100, 10, 1)
	s.Place()
	// Task 1: nodes 2,3 tie at 5 free -> id 2. Task 2: node 3 (5 free).
	if info := taskState(t, s, 1); info.NodeID != 2 {
		t.Fatalf("task 1 on node %d, want 2", info.NodeID)
	}
	if info := taskState(t, s, 2); info.NodeID != 3 {
		t.Fatalf("task 2 on node %d, want 3", info.NodeID)
	}
	// Task 3: node 2 has 4 free, node 3 has 4 free, tie -> id 2.
	if info := taskState(t, s, 3); info.NodeID != 2 {
		t.Fatalf("task 3 on node %d, want 2", info.NodeID)
	}
}

// TestPlacementPriorityOrder: pending tasks place by (prio desc, id asc).
func TestPlacementPriorityOrder(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddTask(t, s, 1, 0, 100, 10, 1)
	mustAddTask(t, s, 2, 5, 100, 10, 1)
	mustAddTask(t, s, 3, 5, 100, 10, 1)
	s.Place()
	// prio 5 first (id 2 then 3) fills both slots; task 1 stays pending.
	if info := taskState(t, s, 1); info.State != Pending {
		t.Fatalf("task 1: %+v, want pending", info)
	}
	for _, id := range []int64{2, 3} {
		if info := taskState(t, s, id); info.State != Running {
			t.Fatalf("task %d: %+v, want running", id, info)
		}
	}
}

// TestNoticedNodeRejectsPlacement: a noticed node takes no new placements.
func TestNoticedNodeRejectsPlacement(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddNode(t, s, 2, Spot, 1)
	mustAddTask(t, s, 1, 0, 1000, 10, 1)
	s.Place()
	if _, _, _, err := s.Notice(1, 0); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	mustAddTask(t, s, 2, 0, 100, 10, 1)
	s.Place()
	// Node 1 has a free slot but is noticed; task 2 must go to node 2.
	if info := taskState(t, s, 2); info.NodeID != 2 {
		t.Fatalf("task 2 on node %d, want 2", info.NodeID)
	}
}

// TestOnDemandCostAndBudget: cost is (w-cp)*Pod at placement time; an
// unaffordable task stays pending without blocking later tasks.
func TestOnDemandCostAndBudget(t *testing.T) {
	s := mustNew(t, 100, 2, 500)
	mustAddNode(t, s, 1, Spot, 1)
	mustAddNode(t, s, 2, OnDemand, 2)
	mustAddTask(t, s, 1, 0, 500, 100, 10) // takes the only spot slot
	mustAddTask(t, s, 2, 0, 400, 100, 10) // cost 400*2=800 > 500: stays pending
	mustAddTask(t, s, 3, 0, 200, 100, 10) // cost 200*2=400 <= 500: placed
	mustAddTask(t, s, 4, 0, 100, 100, 10) // cost 100*2=200 > 100 left: pending
	s.Place()
	if info := taskState(t, s, 1); info.State != Running || info.NodeID != 1 {
		t.Fatalf("task 1: %+v, want running on node 1", info)
	}
	if info := taskState(t, s, 2); info.State != Pending {
		t.Fatalf("task 2: %+v, want pending (cost 800 > budget 500)", info)
	}
	if info := taskState(t, s, 3); info.State != Running || info.NodeID != 2 {
		t.Fatalf("task 3: %+v, want running on node 2 (not blocked by task 2)", info)
	}
	if info := taskState(t, s, 4); info.State != Pending {
		t.Fatalf("task 4: %+v, want pending (cost 200 > budget 100)", info)
	}
	if got := s.Budget(); got != 100 {
		t.Fatalf("Budget=%d, want 100", got)
	}
	// Task 2 never blocked task 3; re-placing changes nothing for task 2.
	s.Place()
	if info := taskState(t, s, 2); info.State != Pending {
		t.Fatalf("task 2 after re-place: %+v, want pending", info)
	}
	if got := s.Budget(); got != 100 {
		t.Fatalf("Budget after re-place=%d, want 100", got)
	}
}

// TestOnDemandCostUsesCheckpointAtPlacement: the charged cost is
// (w-cp)*Pod with cp as of the placement moment.
func TestOnDemandCostUsesCheckpointAtPlacement(t *testing.T) {
	s := mustNew(t, 100, 2, 1000)
	mustAddNode(t, s, 1, Spot, 1)
	mustAddTask(t, s, 1, 0, 500, 100, 10)
	s.Place()
	mustReport(t, s, 1, 350) // cp=300
	if _, _, _, err := s.Notice(1, 0); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if _, err := s.Expire(1, 100); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	mustAddNode(t, s, 2, OnDemand, 1)
	s.Place()
	// Notice saved the task (u=50, ck=10 <= G): cp advanced to p=350.
	// cost = (500-350)*2 = 300
	if got := s.Budget(); got != 700 {
		t.Fatalf("Budget=%d, want 700 (charged (500-350)*2=300)", got)
	}
	if info := taskState(t, s, 1); info.State != Running || info.NodeID != 2 {
		t.Fatalf("task 1: %+v, want running on node 2", info)
	}
}

// TestRs2OnlyOnDemand: a task with rs >= 2 has no Spot candidates and goes
// straight to OnDemand.
func TestRs2OnlyOnDemand(t *testing.T) {
	s := mustNew(t, 100, 2, 2000)
	mustAddNode(t, s, 1, Spot, 1)
	mustAddTask(t, s, 1, 0, 1000, 100, 5)
	s.Place()
	mustReport(t, s, 1, 300) // cp=300
	// First eviction: rs -> 1.
	if _, _, _, err := s.Notice(1, 0); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if _, err := s.Expire(1, 100); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	// Second eviction: rs -> 2.
	mustAddNode(t, s, 2, Spot, 1)
	s.Place()
	if info := taskState(t, s, 1); info.NodeID != 2 {
		t.Fatalf("task 1 on node %d, want 2", info.NodeID)
	}
	if _, _, _, err := s.Notice(2, 0); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if _, err := s.Expire(2, 100); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if info := taskState(t, s, 1); info.Rs != 2 {
		t.Fatalf("task 1 rs=%d, want 2", info.Rs)
	}
	// rs=2: a free Spot node is ignored; OnDemand is used directly.
	mustAddNode(t, s, 3, Spot, 1)
	mustAddNode(t, s, 4, OnDemand, 1)
	s.Place()
	info := taskState(t, s, 1)
	if info.State != Running || info.NodeID != 4 {
		t.Fatalf("task 1: %+v, want running on on-demand node 4", info)
	}
	// cost = (1000-300)*2 = 1400, budget 2000 -> 600 left.
	if got := s.Budget(); got != 600 {
		t.Fatalf("Budget=%d, want 600 (charged (1000-300)*2=1400)", got)
	}
	// The free Spot node 3 stays empty: rs=2 tasks never use Spot.
	if ni, ok := s.NodeInfo(3); !ok || ni.Used != 0 {
		t.Fatalf("node 3: %+v ok=%v, want empty", ni, ok)
	}
}

// TestInvalidConfig: out-of-range constructor parameters reject the whole
// configuration.
func TestInvalidConfig(t *testing.T) {
	cases := [][3]int64{
		{0, 1, 0}, {1_000_001, 1, 0}, {-1, 1, 0},
		{1, 0, 0}, {1, 1_000_001, 0}, {1, -1, 0},
		{1, 1, -1}, {1, 1, 1_000_000_000_001},
	}
	for _, c := range cases {
		if _, err := NewScheduler(c[0], c[1], c[2]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewScheduler%v: err=%v, want ErrInvalidConfig", c, err)
		}
	}
	if _, err := NewScheduler(1, 1, 0); err != nil {
		t.Fatalf("NewScheduler(1,1,0): %v", err)
	}
	if _, err := NewScheduler(1_000_000, 1_000_000, 1_000_000_000_000); err != nil {
		t.Fatalf("NewScheduler(max,max,max): %v", err)
	}
}

// TestErrorOrdering: each operation reports the first failure in its
// documented check order, and rejected operations change nothing.
func TestErrorOrdering(t *testing.T) {
	s := mustNew(t, 100, 2, 500)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddNode(t, s, 2, OnDemand, 1)
	mustAddTask(t, s, 1, 0, 500, 100, 10)

	// AddNode: invalid params before duplicate id.
	if err := s.AddNode(1, Spot, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddNode dup+invalid: %v", err)
	}
	if err := s.AddNode(1, Spot, 1); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("AddNode dup: %v", err)
	}
	if err := s.AddNode(0, Spot, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddNode id=0: %v", err)
	}
	if err := s.AddNode(5, NodeKind(7), 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddNode bad kind: %v", err)
	}
	if err := s.AddNode(5, Spot, 1001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddNode slots=1001: %v", err)
	}

	// AddTask: invalid params before duplicate id.
	if err := s.AddTask(1, 0, 0, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddTask dup+invalid: %v", err)
	}
	if err := s.AddTask(1, 0, 1, 1, 1); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("AddTask dup: %v", err)
	}
	if err := s.AddTask(2, 256, 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddTask prio=256: %v", err)
	}
	if err := s.AddTask(2, 0, 1_000_000_001, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddTask w too big: %v", err)
	}

	// Notice: bad now, unknown node, not spot, already noticed.
	if _, _, _, err := s.Notice(1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Notice bad now: %v", err)
	}
	if _, _, _, err := s.Notice(99, 0); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Notice unknown: %v", err)
	}
	if _, _, _, err := s.Notice(2, 0); !errors.Is(err, ErrNotSpot) {
		t.Fatalf("Notice on-demand: %v", err)
	}
	if _, _, _, err := s.Notice(1, 0); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if _, _, _, err := s.Notice(1, 0); !errors.Is(err, ErrAlreadyNoticed) {
		t.Fatalf("Notice again: %v", err)
	}

	// Expire: bad now, unknown node, not noticed, not expired.
	if _, err := s.Expire(1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Expire bad now: %v", err)
	}
	if _, err := s.Expire(99, 0); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Expire unknown: %v", err)
	}
	if _, err := s.Expire(2, 0); !errors.Is(err, ErrNotNoticed) {
		t.Fatalf("Expire not noticed: %v", err)
	}
	if _, err := s.Expire(1, 99); !errors.Is(err, ErrNotExpired) {
		t.Fatalf("Expire early: %v", err)
	}
	if _, err := s.Expire(1, 100); err != nil {
		t.Fatalf("Expire at dl: %v", err)
	}
	if _, err := s.Expire(1, 100); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Expire removed node: %v", err)
	}

	// Report: unknown task, not running, invalid progress.
	if err := s.Report(99, 0); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Report unknown: %v", err)
	}
	if err := s.Report(1, 0); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Report pending: %v", err)
	}
	s.Place() // task 1 -> node 2 (on-demand), cost (500-0)*2=1000 > 500: stays pending
	if err := s.Report(1, 0); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Report still pending: %v", err)
	}
	mustAddNode(t, s, 3, Spot, 1)
	s.Place()
	mustReport(t, s, 1, 100)
	if err := s.Report(1, 50); !errors.Is(err, ErrInvalidProgress) {
		t.Fatalf("Report backwards: %v", err)
	}
	if err := s.Report(1, 501); !errors.Is(err, ErrInvalidProgress) {
		t.Fatalf("Report beyond w: %v", err)
	}
	mustReport(t, s, 1, 500)
	if err := s.Report(1, 500); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Report completed: %v", err)
	}
}

// TestRejectedOpsKeepState: rejected operations mutate nothing.
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustNew(t, 100, 2, 500)
	mustAddNode(t, s, 1, Spot, 1)
	mustAddTask(t, s, 1, 0, 500, 100, 10)
	s.Place()
	mustReport(t, s, 1, 250)
	before := taskState(t, s, 1)
	beforeBud := s.Budget()

	// A batch of rejected calls.
	_ = s.AddNode(1, Spot, 1)
	_ = s.AddNode(-1, Spot, 1)
	_ = s.AddTask(1, 0, 1, 1, 1)
	_ = s.AddTask(2, 999, 1, 1, 1)
	_, _, _, _ = s.Notice(1, -5)
	_, _, _, _ = s.Notice(77, 0)
	_, _ = s.Expire(1, 0)  // not noticed
	_, _ = s.Expire(88, 0) // unknown
	_ = s.Report(1, 100)   // backwards
	_ = s.Report(1, 99999) // beyond w
	_ = s.Report(55, 0)    // unknown task

	after := taskState(t, s, 1)
	if after != before {
		t.Fatalf("task state changed by rejected ops: %+v -> %+v", before, after)
	}
	if got := s.Budget(); got != beforeBud {
		t.Fatalf("budget changed by rejected ops: %d -> %d", beforeBud, got)
	}
	if info, ok := s.NodeInfo(1); !ok || info.Used != 1 || info.Noticed {
		t.Fatalf("node state changed by rejected ops: %+v ok=%v", info, ok)
	}
	if _, ok := s.TaskInfo(2); ok {
		t.Fatal("rejected AddTask created task 2")
	}
	if _, ok := s.NodeInfo(2); ok {
		t.Fatal("rejected AddNode created node 2")
	}
}

// TestConcurrentUse: operations and queries from many goroutines are
// serialized by the scheduler; run with -race.
func TestConcurrentUse(t *testing.T) {
	s := mustNew(t, 50, 2, 1_000_000)
	for id := int64(1); id <= 4; id++ {
		mustAddNode(t, s, id, Spot, 2)
	}
	mustAddNode(t, s, 9, OnDemand, 4)
	for id := int64(1); id <= 16; id++ {
		mustAddTask(t, s, id, id%4, 500, 50, 10)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := int64((worker*200+i)%16 + 1)
				s.Place()
				_ = s.Report(id, int64(i))
				_, _ = s.TaskInfo(id)
				_, _ = s.NodeInfo(id%4 + 1)
				_ = s.Budget()
				_ = s.TotalRework()
			}
		}(worker)
	}
	for n := int64(1); n <= 4; n++ {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			if _, _, _, err := s.Notice(n, 0); err == nil {
				_, _ = s.Expire(n, 50)
			}
		}(n)
	}
	wg.Wait()
	// Invariants after the storm: p >= cp everywhere, budget non-negative,
	// slot counts within capacity.
	for id := int64(1); id <= 16; id++ {
		info := taskState(t, s, id)
		if info.P < info.Cp {
			t.Fatalf("task %d: p=%d < cp=%d", id, info.P, info.Cp)
		}
	}
	if s.Budget() < 0 {
		t.Fatalf("negative budget %d", s.Budget())
	}
	for id := int64(1); id <= 9; id++ {
		if info, ok := s.NodeInfo(id); ok && info.Used > info.Slots {
			t.Fatalf("node %d: used %d > slots %d", id, info.Used, info.Slots)
		}
	}
}

// TestUDescTieByID: equal u values are processed by ascending id.
func TestUDescTieByID(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	mustAddNode(t, s, 1, Spot, 2)
	mustAddTask(t, s, 1, 0, 1000, 10, 6)
	mustAddTask(t, s, 2, 0, 1000, 10, 5)
	s.Place()
	mustReport(t, s, 1, 105) // u=5
	mustReport(t, s, 2, 105) // u=5
	_, saved, dropped, err := s.Notice(1, 0)
	if err != nil {
		t.Fatalf("Notice: %v", err)
	}
	// id 1 first: cumulative 6 <= 10 saved; id 2: 11 > 10 dropped.
	if !reflect.DeepEqual(saved, []int64{1}) {
		t.Fatalf("saved=%v, want [1]", saved)
	}
	if !reflect.DeepEqual(dropped, []int64{2}) {
		t.Fatalf("dropped=%v, want [2]", dropped)
	}
}
