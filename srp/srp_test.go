package srp

import (
	"errors"
	"reflect"
	"testing"
)

func mustDeclare(t *testing.T, m *Manager, id string, n int) {
	t.Helper()
	if err := m.DeclareResource(id, n); err != nil {
		t.Fatalf("DeclareResource(%q,%d): %v", id, n, err)
	}
}

func mustAddTask(t *testing.T, m *Manager, id string, d int, mu ...MuEntry) {
	t.Helper()
	if err := m.AddTask(id, d, mu); err != nil {
		t.Fatalf("AddTask(%q,%d): %v", id, d, err)
	}
}

func mustStart(t *testing.T, m *Manager, job, task string) {
	t.Helper()
	if err := m.Start(job, task); err != nil {
		t.Fatalf("Start(%q,%q): %v", job, task, err)
	}
}

func mustAcquire(t *testing.T, m *Manager, job, res string, u int) {
	t.Helper()
	if err := m.Acquire(job, res, u); err != nil {
		t.Fatalf("Acquire(%q,%q,%d): %v", job, res, u, err)
	}
}

func mustRelease(t *testing.T, m *Manager, job, res string, u int) {
	t.Helper()
	if err := m.Release(job, res, u); err != nil {
		t.Fatalf("Release(%q,%q,%d): %v", job, res, u, err)
	}
}

func mustFinish(t *testing.T, m *Manager, job string) {
	t.Helper()
	if err := m.Finish(job); err != nil {
		t.Fatalf("Finish(%q): %v", job, err)
	}
}

func expectErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func mustLevel(t *testing.T, m *Manager, task string, want int) {
	t.Helper()
	got, err := m.Level(task)
	if err != nil {
		t.Fatalf("Level(%q): %v", task, err)
	}
	if got != want {
		t.Fatalf("Level(%q) = %d, want %d", task, got, want)
	}
}

func mustCeil(t *testing.T, m *Manager, res string, want int) {
	t.Helper()
	got, err := m.Ceil(res)
	if err != nil {
		t.Fatalf("Ceil(%q): %v", res, err)
	}
	if got != want {
		t.Fatalf("Ceil(%q) = %d, want %d", res, got, want)
	}
}

func mustSysCeil(t *testing.T, m *Manager, want int) {
	t.Helper()
	if got := m.SysCeil(); got != want {
		t.Fatalf("SysCeil() = %d, want %d", got, want)
	}
}

func mustAvail(t *testing.T, m *Manager, res string, want int) {
	t.Helper()
	got, err := m.Avail(res)
	if err != nil {
		t.Fatalf("Avail(%q): %v", res, err)
	}
	if got != want {
		t.Fatalf("Avail(%q) = %d, want %d", res, got, want)
	}
}

func mustStack(t *testing.T, m *Manager, want ...string) {
	t.Helper()
	got := m.Stack()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stack() = %v, want %v", got, want)
	}
}

// checkInvariants verifies the global invariants of the manager.
func checkInvariants(t *testing.T, m *Manager) {
	t.Helper()
	// Stack levels strictly increase from bottom to top.
	for i := 1; i < len(m.stack); i++ {
		prev := m.tasks[m.stack[i-1].taskID]
		cur := m.tasks[m.stack[i].taskID]
		if m.levelOf(prev.d) >= m.levelOf(cur.d) {
			t.Fatalf("stack level inversion: %q(pi=%d) below %q(pi=%d)",
				prev.id, m.levelOf(prev.d), cur.id, m.levelOf(cur.d))
		}
	}
	// avail + sum(held) == n for every resource.
	for _, id := range m.resOrder {
		r := m.resources[id]
		held := 0
		for _, j := range m.stack {
			held += j.held[id]
		}
		if r.avail+held != r.n {
			t.Fatalf("resource %q: avail %d + held %d != n %d", id, r.avail, held, r.n)
		}
		if r.avail < 0 {
			t.Fatalf("resource %q: negative avail %d", id, r.avail)
		}
	}
}

// newExample builds the scenario from the problem statement:
// resource R (N=3); tasks A (D=20, mu_R=3), B (D=10, mu_R=1), C (D=5, mu_R=2).
func newExample(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	mustDeclare(t, m, "R", 3)
	mustAddTask(t, m, "A", 20, MuEntry{Resource: "R", Units: 3})
	mustAddTask(t, m, "B", 10, MuEntry{Resource: "R", Units: 1})
	mustAddTask(t, m, "C", 5, MuEntry{Resource: "R", Units: 2})
	return m
}

func TestExampleScenario(t *testing.T) {
	m := newExample(t)

	mustLevel(t, m, "A", 1)
	mustLevel(t, m, "B", 2)
	mustLevel(t, m, "C", 3)
	mustSysCeil(t, m, 0)

	mustStart(t, m, "a1", "A")
	mustAcquire(t, m, "a1", "R", 2)
	mustAvail(t, m, "R", 1)
	mustCeil(t, m, "R", 3)
	mustSysCeil(t, m, 3)

	expectErr(t, m.Start("c1", "C"), ErrCeilingBlocked)
	expectErr(t, m.Start("b1", "B"), ErrCeilingBlocked)

	mustRelease(t, m, "a1", "R", 1)
	mustAvail(t, m, "R", 2)
	mustSysCeil(t, m, 1)

	mustStart(t, m, "b1", "B")
	mustAcquire(t, m, "b1", "R", 1)
	mustAvail(t, m, "R", 1)
	mustSysCeil(t, m, 3)
	expectErr(t, m.Start("c1", "C"), ErrCeilingBlocked)

	mustRelease(t, m, "b1", "R", 1)
	mustSysCeil(t, m, 1)
	mustStart(t, m, "c1", "C")
	mustStack(t, m, "a1", "b1", "c1")

	expectErr(t, m.Finish("b1"), ErrNotTop)
	mustStack(t, m, "a1", "b1", "c1")
	checkInvariants(t, m)
}

func TestCeilingStrictInequality(t *testing.T) {
	// mu == avail does not enter the ceiling; mu == avail+1 does.
	m := NewManager()
	mustDeclare(t, m, "R", 4)
	mustAddTask(t, m, "EQ", 10, MuEntry{Resource: "R", Units: 4}) // pi=1, mu == N
	mustAddTask(t, m, "GT", 5, MuEntry{Resource: "R", Units: 3})  // pi=2

	// avail=4: EQ's mu==4 is not > 4, GT's 3 is not > 4: ceiling 0.
	mustCeil(t, m, "R", 0)

	// Drive avail down to 4 by acquiring from a task with mu 4... use EQ itself.
	mustStart(t, m, "eq1", "EQ")
	mustAcquire(t, m, "eq1", "R", 1) // avail=3: EQ mu 4 > 3 -> ceil 1; GT 3 not > 3
	mustCeil(t, m, "R", 1)
	mustSysCeil(t, m, 1)

	mustAcquire(t, m, "eq1", "R", 1) // avail=2: GT mu 3 > 2 -> ceil 2
	mustCeil(t, m, "R", 2)
	mustSysCeil(t, m, 2)

	mustRelease(t, m, "eq1", "R", 1) // avail=3: GT 3 not > 3 -> back to 1
	mustCeil(t, m, "R", 1)
	checkInvariants(t, m)
}

func TestSameLevelCannotPreempt(t *testing.T) {
	// Two tasks with equal deadlines share pi and cannot preempt each other.
	m := NewManager()
	mustDeclare(t, m, "R", 2)
	mustAddTask(t, m, "T1", 10, MuEntry{Resource: "R", Units: 1})
	mustAddTask(t, m, "T2", 10, MuEntry{Resource: "R", Units: 1})
	mustLevel(t, m, "T1", 1)
	mustLevel(t, m, "T2", 1)

	mustStart(t, m, "j1", "T1")
	expectErr(t, m.Start("j2", "T2"), ErrLevelInsufficient)
	mustStack(t, m, "j1")
	checkInvariants(t, m)
}

func TestInsertMiddleDeadlineShiftsLevels(t *testing.T) {
	// Adding a task with a middle deadline re-derives pi for all tasks,
	// but the relative order of levels on the stack is preserved.
	m := NewManager()
	mustDeclare(t, m, "R", 10)
	mustAddTask(t, m, "HI", 30, MuEntry{Resource: "R", Units: 1})
	mustAddTask(t, m, "LO", 10, MuEntry{Resource: "R", Units: 1})
	mustLevel(t, m, "HI", 1)
	mustLevel(t, m, "LO", 2)

	mustStart(t, m, "j1", "HI")
	mustStart(t, m, "j2", "LO")

	// Insert MID with D=20: HI stays 1, MID becomes 2, LO shifts to 3.
	mustAddTask(t, m, "MID", 20, MuEntry{Resource: "R", Units: 1})
	mustLevel(t, m, "HI", 1)
	mustLevel(t, m, "MID", 2)
	mustLevel(t, m, "LO", 3)
	mustStack(t, m, "j1", "j2")
	checkInvariants(t, m)

	// Removing MID shifts LO back to 2.
	if err := m.RemoveTask("MID"); err != nil {
		t.Fatalf("RemoveTask(MID): %v", err)
	}
	mustLevel(t, m, "LO", 2)
	checkInvariants(t, m)
}

func TestMultiResourceMaxCeiling(t *testing.T) {
	m := NewManager()
	mustDeclare(t, m, "R1", 2)
	mustDeclare(t, m, "R2", 2)
	mustAddTask(t, m, "A", 30, MuEntry{Resource: "R1", Units: 2})
	mustAddTask(t, m, "B", 20, MuEntry{Resource: "R2", Units: 2})
	mustAddTask(t, m, "C", 10, MuEntry{Resource: "R1", Units: 1}, MuEntry{Resource: "R2", Units: 1})

	mustStart(t, m, "a1", "A")
	mustAcquire(t, m, "a1", "R1", 1) // avail R1=1: A(2)>1 -> ceil R1=1
	mustCeil(t, m, "R1", 1)
	mustCeil(t, m, "R2", 0)
	mustSysCeil(t, m, 1)

	mustAcquire(t, m, "a1", "R1", 1) // avail R1=0: C(1)>0 too -> ceil R1=3
	mustCeil(t, m, "R1", 3)
	mustSysCeil(t, m, 3)

	// Free R1, then exhaust R2 via C's job to raise R2's ceiling instead.
	mustRelease(t, m, "a1", "R1", 2)
	mustSysCeil(t, m, 0)
	mustFinish(t, m, "a1")

	mustStart(t, m, "b1", "B")
	mustAcquire(t, m, "b1", "R2", 1) // avail R2=1: B(2)>1 -> ceil R2=2
	mustCeil(t, m, "R2", 2)
	mustSysCeil(t, m, 2)
	mustAcquire(t, m, "b1", "R2", 1) // avail R2=0: C(1)>0 -> ceil R2=3
	mustSysCeil(t, m, 3)
	checkInvariants(t, m)
}

func TestCeilingDropsAfterRelease(t *testing.T) {
	m := newExample(t)
	mustStart(t, m, "a1", "A")
	mustAcquire(t, m, "a1", "R", 2)
	mustSysCeil(t, m, 3)
	mustRelease(t, m, "a1", "R", 2)
	mustSysCeil(t, m, 0)
	checkInvariants(t, m)
}

func TestUnlistedResourceMuZero(t *testing.T) {
	// A resource not listed in the task's mu table has mu = 0, so any
	// acquire of it exceeds the declaration.
	m := NewManager()
	mustDeclare(t, m, "R1", 5)
	mustDeclare(t, m, "R2", 5)
	mustAddTask(t, m, "T", 10, MuEntry{Resource: "R1", Units: 2})
	mustStart(t, m, "j1", "T")
	expectErr(t, m.Acquire("j1", "R2", 1), ErrExceedsDeclared)
	mustAvail(t, m, "R2", 5)
	checkInvariants(t, m)
}

func TestNonTopReleaseAndFinishRejected(t *testing.T) {
	m := newExample(t)
	mustStart(t, m, "a1", "A")
	mustAcquire(t, m, "a1", "R", 1)
	mustRelease(t, m, "a1", "R", 1)
	mustStart(t, m, "b1", "B")

	expectErr(t, m.Release("a1", "R", 1), ErrNotTop)
	expectErr(t, m.Acquire("a1", "R", 1), ErrNotTop)
	expectErr(t, m.Finish("a1"), ErrNotTop)
	mustStack(t, m, "a1", "b1")
	checkInvariants(t, m)
}

func TestRejectionReasonsAndNoStateChange(t *testing.T) {
	m := newExample(t)

	// Invalid parameters.
	expectErr(t, m.DeclareResource("", 1), ErrInvalidParam)
	expectErr(t, m.DeclareResource("X", 0), ErrInvalidParam)
	expectErr(t, m.DeclareResource("X", 1001), ErrInvalidParam)
	expectErr(t, m.AddTask("", 1, nil), ErrInvalidParam)
	expectErr(t, m.AddTask("X", 0, nil), ErrInvalidParam)
	expectErr(t, m.AddTask("X", 1_000_001, nil), ErrInvalidParam)
	expectErr(t, m.AddTask("X", 1, []MuEntry{{Resource: "R", Units: 0}}), ErrInvalidParam)
	expectErr(t, m.AddTask("X", 1,
		[]MuEntry{{Resource: "R", Units: 1}, {Resource: "R", Units: 1}}), ErrInvalidParam)
	expectErr(t, m.Start("", "A"), ErrInvalidParam)
	expectErr(t, m.Acquire("j", "R", 0), ErrInvalidParam)
	expectErr(t, m.Acquire("j", "R", 1001), ErrInvalidParam)

	// Not found.
	expectErr(t, m.AddTask("X", 1, []MuEntry{{Resource: "ZZ", Units: 1}}), ErrNotFound)
	expectErr(t, m.RemoveTask("ZZ"), ErrNotFound)
	expectErr(t, m.Start("j1", "ZZ"), ErrNotFound)
	expectErr(t, m.Acquire("zz", "R", 1), ErrNotFound)
	expectErr(t, m.Finish("zz"), ErrNotFound)
	_, err := m.Level("ZZ")
	expectErr(t, err, ErrNotFound)
	_, err = m.Ceil("ZZ")
	expectErr(t, err, ErrNotFound)
	_, err = m.Avail("ZZ")
	expectErr(t, err, ErrNotFound)

	// mu larger than the resource total is invalid.
	expectErr(t, m.AddTask("X", 1, []MuEntry{{Resource: "R", Units: 4}}), ErrInvalidParam)

	// Duplicate identifiers.
	expectErr(t, m.DeclareResource("R", 1), ErrDuplicate)
	expectErr(t, m.AddTask("A", 99, nil), ErrDuplicate)

	mustStart(t, m, "a1", "A")
	expectErr(t, m.Start("a1", "B"), ErrDuplicate)

	// Level insufficient vs ceiling blocked: with avail=3, SysCeil=0, so
	// starting C (pi=3) over A (pi=1) passes the level check but a second
	// job of B (pi=2) over C... first exhaust resources to raise the ceiling.
	mustAcquire(t, m, "a1", "R", 2)                     // avail=1, SysCeil=3
	expectErr(t, m.Start("b1", "B"), ErrCeilingBlocked) // 2 <= 3
	expectErr(t, m.Start("c1", "C"), ErrCeilingBlocked) // 3 <= 3
	mustRelease(t, m, "a1", "R", 2)                     // avail=3, SysCeil=0

	// Acquire beyond declared, then beyond availability.
	mustAcquire(t, m, "a1", "R", 3) // A declares 3
	expectErr(t, m.Acquire("a1", "R", 1), ErrExceedsDeclared)
	mustRelease(t, m, "a1", "R", 3)

	// Release more than held.
	expectErr(t, m.Release("a1", "R", 1), ErrNotHeld)

	// Finish while holding.
	mustAcquire(t, m, "a1", "R", 1)
	expectErr(t, m.Finish("a1"), ErrStillHolding)

	// RemoveTask while a job of the task is running.
	expectErr(t, m.RemoveTask("A"), ErrTaskInUse)

	mustRelease(t, m, "a1", "R", 1)
	mustFinish(t, m, "a1")
	if err := m.RemoveTask("A"); err != nil {
		t.Fatalf("RemoveTask(A): %v", err)
	}
	mustStack(t, m)
	checkInvariants(t, m)
}

func TestCapacityLimits(t *testing.T) {
	m := NewManager()
	for i := 0; i < MaxResources; i++ {
		mustDeclare(t, m, string(rune('a'+i)), 1)
	}
	expectErr(t, m.DeclareResource("overflow", 1), ErrCapacity)

	for i := 0; i < MaxTasks; i++ {
		mustAddTask(t, m, string(rune('A'+i)), i+1)
	}
	expectErr(t, m.AddTask("overflow", 99, nil), ErrCapacity)

	// 16 nested jobs, starting from the largest deadline (pi=1) down;
	// with at most 16 tasks the 32-job capacity is never the binding limit.
	for i := 0; i < MaxTasks; i++ {
		mustStart(t, m, "job-"+string(rune('a'+i)), string(rune('A'+MaxTasks-1-i)))
	}
	if got := len(m.Stack()); got != MaxTasks {
		t.Fatalf("stack depth = %d, want %d", got, MaxTasks)
	}
	checkInvariants(t, m)
}

func TestLevelInsufficientDistinctFromCeiling(t *testing.T) {
	// With an empty system ceiling, a job whose level is not greater than
	// the stack top's level is rejected with ErrLevelInsufficient.
	m := newExample(t)
	mustStart(t, m, "b1", "B")                             // pi=2 on empty stack
	expectErr(t, m.Start("a1", "A"), ErrLevelInsufficient) // pi=1 <= 2
	expectErr(t, m.Start("b2", "B"), ErrLevelInsufficient) // pi=2 <= 2
	mustStart(t, m, "c1", "C")                             // pi=3 > 2
	mustStack(t, m, "b1", "c1")
	checkInvariants(t, m)
}
