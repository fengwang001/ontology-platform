// Differential test: the Manager is compared, operation by operation,
// against a naive model written directly from the problem statement.
// Every generated operation is logged with its input, its output and the
// reason for the decision.
package srp

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// --- naive model, intentionally independent from the Manager ---

type modelTask struct {
	d  int
	mu map[string]int
}

type modelJob struct {
	id     string
	taskID string
	held   map[string]int
}

type model struct {
	total map[string]int
	avail map[string]int
	tasks map[string]modelTask
	jobs  map[string]bool
	stack []modelJob
}

func newModel() *model {
	return &model{
		total: map[string]int{},
		avail: map[string]int{},
		tasks: map[string]modelTask{},
		jobs:  map[string]bool{},
	}
}

// level computes pi literally: collect the distinct deadlines of all
// tasks, sort them descending, and rank the task's deadline among them.
func (m *model) level(taskID string) int {
	ds := map[int]bool{}
	for _, t := range m.tasks {
		ds[t.d] = true
	}
	sorted := make([]int, 0, len(ds))
	for d := range ds {
		sorted = append(sorted, d)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	for i, d := range sorted {
		if d == m.tasks[taskID].d {
			return i + 1
		}
	}
	return 0
}

// ceil computes max{ pi_k : mu_{k,r} > avail_r } with a strict inequality.
func (m *model) ceil(res string) int {
	ceil := 0
	for id, t := range m.tasks {
		if t.mu[res] > m.avail[res] {
			if lv := m.level(id); lv > ceil {
				ceil = lv
			}
		}
	}
	return ceil
}

func (m *model) sysCeil() int {
	ceil := 0
	for r := range m.total {
		if c := m.ceil(r); c > ceil {
			ceil = c
		}
	}
	return ceil
}

// Each model operation returns the rejection (nil on success) plus a
// human-readable rationale naming the check that decided the outcome.

func (m *model) declareResource(id string, n int) (error, string) {
	if id == "" || n < 1 || n > MaxUnits {
		return ErrInvalidParam, fmt.Sprintf("invalid param: id=%q n=%d", id, n)
	}
	if _, ok := m.total[id]; ok {
		return ErrDuplicate, fmt.Sprintf("duplicate resource %q", id)
	}
	if len(m.total) >= MaxResources {
		return ErrCapacity, "resource capacity reached"
	}
	m.total[id] = n
	m.avail[id] = n
	return nil, fmt.Sprintf("ok: resource %q n=%d", id, n)
}

func (m *model) addTask(id string, d int, mu []MuEntry) (error, string) {
	if id == "" || d < 1 || d > MaxDeadline || len(mu) > MaxMuEntries {
		return ErrInvalidParam, fmt.Sprintf("invalid param: id=%q d=%d entries=%d", id, d, len(mu))
	}
	seen := map[string]bool{}
	for _, e := range mu {
		if e.Resource == "" || e.Units < 1 || e.Units > MaxUnits {
			return ErrInvalidParam, fmt.Sprintf("invalid param: mu entry %+v", e)
		}
		if seen[e.Resource] {
			return ErrInvalidParam, fmt.Sprintf("invalid param: duplicate mu resource %q", e.Resource)
		}
		seen[e.Resource] = true
	}
	for _, e := range mu {
		n, ok := m.total[e.Resource]
		if !ok {
			return ErrNotFound, fmt.Sprintf("resource %q not found", e.Resource)
		}
		if e.Units > n {
			return ErrInvalidParam, fmt.Sprintf("invalid param: mu %d > total %d of %q", e.Units, n, e.Resource)
		}
	}
	if _, ok := m.tasks[id]; ok {
		return ErrDuplicate, fmt.Sprintf("duplicate task %q", id)
	}
	if len(m.tasks) >= MaxTasks {
		return ErrCapacity, "task capacity reached"
	}
	table := map[string]int{}
	for _, e := range mu {
		table[e.Resource] = e.Units
	}
	m.tasks[id] = modelTask{d: d, mu: table}
	return nil, fmt.Sprintf("ok: task %q d=%d", id, d)
}

func (m *model) removeTask(id string) (error, string) {
	if id == "" {
		return ErrInvalidParam, "invalid param: empty task id"
	}
	if _, ok := m.tasks[id]; !ok {
		return ErrNotFound, fmt.Sprintf("task %q not found", id)
	}
	for _, j := range m.stack {
		if j.taskID == id {
			return ErrTaskInUse, fmt.Sprintf("task %q in use by job %q", id, j.id)
		}
	}
	delete(m.tasks, id)
	return nil, fmt.Sprintf("ok: removed task %q", id)
}

func (m *model) start(jobID, taskID string) (error, string) {
	if jobID == "" || taskID == "" {
		return ErrInvalidParam, "invalid param: empty id"
	}
	if _, ok := m.tasks[taskID]; !ok {
		return ErrNotFound, fmt.Sprintf("task %q not found", taskID)
	}
	if m.jobs[jobID] {
		return ErrDuplicate, fmt.Sprintf("duplicate job %q", jobID)
	}
	if len(m.stack) >= MaxJobs {
		return ErrCapacity, "job capacity reached"
	}
	level := m.level(taskID)
	if n := len(m.stack); n > 0 {
		topLevel := m.level(m.stack[n-1].taskID)
		if level <= topLevel {
			return ErrLevelInsufficient, fmt.Sprintf("level %d <= stack top level %d", level, topLevel)
		}
	}
	if ceil := m.sysCeil(); level <= ceil {
		return ErrCeilingBlocked, fmt.Sprintf("level %d <= system ceiling %d", level, ceil)
	}
	m.stack = append(m.stack, modelJob{id: jobID, taskID: taskID, held: map[string]int{}})
	m.jobs[jobID] = true
	return nil, fmt.Sprintf("ok: pushed job %q at level %d", jobID, level)
}

func (m *model) acquire(jobID, res string, u int) (error, string) {
	if jobID == "" || res == "" || u < 1 || u > MaxUnits {
		return ErrInvalidParam, fmt.Sprintf("invalid param: job=%q res=%q u=%d", jobID, res, u)
	}
	if !m.jobs[jobID] {
		return ErrNotFound, fmt.Sprintf("job %q not found", jobID)
	}
	if _, ok := m.total[res]; !ok {
		return ErrNotFound, fmt.Sprintf("resource %q not found", res)
	}
	top := &m.stack[len(m.stack)-1]
	if top.id != jobID {
		return ErrNotTop, fmt.Sprintf("job %q is not stack top %q", jobID, top.id)
	}
	declared := m.tasks[top.taskID].mu[res]
	if top.held[res]+u > declared {
		return ErrExceedsDeclared, fmt.Sprintf("held %d + %d > declared %d", top.held[res], u, declared)
	}
	if u > m.avail[res] {
		return ErrInsufficientUnits, fmt.Sprintf("want %d, only %d available", u, m.avail[res])
	}
	m.avail[res] -= u
	top.held[res] += u
	return nil, fmt.Sprintf("ok: acquired %d of %q, avail=%d", u, res, m.avail[res])
}

func (m *model) release(jobID, res string, u int) (error, string) {
	if jobID == "" || res == "" || u < 1 || u > MaxUnits {
		return ErrInvalidParam, fmt.Sprintf("invalid param: job=%q res=%q u=%d", jobID, res, u)
	}
	if !m.jobs[jobID] {
		return ErrNotFound, fmt.Sprintf("job %q not found", jobID)
	}
	if _, ok := m.total[res]; !ok {
		return ErrNotFound, fmt.Sprintf("resource %q not found", res)
	}
	top := &m.stack[len(m.stack)-1]
	if top.id != jobID {
		return ErrNotTop, fmt.Sprintf("job %q is not stack top %q", jobID, top.id)
	}
	if u > top.held[res] {
		return ErrNotHeld, fmt.Sprintf("holds %d of %q, cannot release %d", top.held[res], res, u)
	}
	top.held[res] -= u
	m.avail[res] += u
	return nil, fmt.Sprintf("ok: released %d of %q, avail=%d", u, res, m.avail[res])
}

func (m *model) finish(jobID string) (error, string) {
	if jobID == "" {
		return ErrInvalidParam, "invalid param: empty job id"
	}
	if !m.jobs[jobID] {
		return ErrNotFound, fmt.Sprintf("job %q not found", jobID)
	}
	top := &m.stack[len(m.stack)-1]
	if top.id != jobID {
		return ErrNotTop, fmt.Sprintf("job %q is not stack top %q", jobID, top.id)
	}
	for res, u := range top.held {
		if u > 0 {
			return ErrStillHolding, fmt.Sprintf("job %q still holds %d of %q", jobID, u, res)
		}
	}
	m.stack = m.stack[:len(m.stack)-1]
	delete(m.jobs, jobID)
	return nil, fmt.Sprintf("ok: popped job %q", jobID)
}

// --- random operation generation ---

type opKind int

const (
	opDeclare opKind = iota
	opAddTask
	opRemoveTask
	opStart
	opAcquire
	opRelease
	opFinish
)

type op struct {
	kind opKind
	a, b string
	n    int
	mu   []MuEntry
}

func (o op) String() string {
	switch o.kind {
	case opDeclare:
		return fmt.Sprintf("DeclareResource(%q, %d)", o.a, o.n)
	case opAddTask:
		return fmt.Sprintf("AddTask(%q, %d, %v)", o.a, o.n, o.mu)
	case opRemoveTask:
		return fmt.Sprintf("RemoveTask(%q)", o.a)
	case opStart:
		return fmt.Sprintf("Start(%q, %q)", o.a, o.b)
	case opAcquire:
		return fmt.Sprintf("Acquire(%q, %q, %d)", o.a, o.b, o.n)
	case opRelease:
		return fmt.Sprintf("Release(%q, %q, %d)", o.a, o.b, o.n)
	case opFinish:
		return fmt.Sprintf("Finish(%q)", o.a)
	}
	return "?"
}

func applyOp(m *Manager, o op) error {
	switch o.kind {
	case opDeclare:
		return m.DeclareResource(o.a, o.n)
	case opAddTask:
		return m.AddTask(o.a, o.n, o.mu)
	case opRemoveTask:
		return m.RemoveTask(o.a)
	case opStart:
		return m.Start(o.a, o.b)
	case opAcquire:
		return m.Acquire(o.a, o.b, o.n)
	case opRelease:
		return m.Release(o.a, o.b, o.n)
	case opFinish:
		return m.Finish(o.a)
	}
	panic("bad op")
}

func applyModel(m *model, o op) (error, string) {
	switch o.kind {
	case opDeclare:
		return m.declareResource(o.a, o.n)
	case opAddTask:
		return m.addTask(o.a, o.n, o.mu)
	case opRemoveTask:
		return m.removeTask(o.a)
	case opStart:
		return m.start(o.a, o.b)
	case opAcquire:
		return m.acquire(o.a, o.b, o.n)
	case opRelease:
		return m.release(o.a, o.b, o.n)
	case opFinish:
		return m.finish(o.a)
	}
	panic("bad op")
}

// idPool is a small set of identifiers so that random operations often
// hit existing objects and sometimes miss.
var idPool = []string{"R1", "R2", "R3", "T1", "T2", "T3", "T4", "J1", "J2", "J3", "J4", "J5", "", "ZZ"}

func pickID(rng *rand.Rand) string { return idPool[rng.Intn(len(idPool))] }

// genSequence builds one random mix of legal and illegal operations.
func genSequence(rng *rand.Rand) []op {
	switch rng.Intn(6) {
	case 0, 1, 2:
		return genDirected(rng)
	case 3:
		return genCapacityStress(rng)
	}
	var ops []op
	// Seed a few resources and tasks with small numbers so ceilings,
	// equal deadlines and capacity limits are all exercised.
	nRes := 1 + rng.Intn(3)
	resN := map[string]int{}
	for i := 0; i < nRes; i++ {
		id := fmt.Sprintf("R%d", i+1)
		n := 1 + rng.Intn(5)
		resN[id] = n
		ops = append(ops, op{kind: opDeclare, a: id, n: n})
	}
	nTask := 1 + rng.Intn(5)
	for i := 0; i < nTask; i++ {
		ops = append(ops, op{kind: opAddTask, a: fmt.Sprintf("T%d", i+1), n: 1 + rng.Intn(6), mu: genMu(rng, resN)})
	}
	for i := 0; i < 20+rng.Intn(40); i++ {
		ops = append(ops, genOp(rng, resN))
	}
	return ops
}

// genCapacityStress overflows the resource and task limits so the
// capacity rejection is exercised, then keeps operating.
func genCapacityStress(rng *rand.Rand) []op {
	var ops []op
	resN := map[string]int{}
	for i := 0; i < MaxResources+2; i++ {
		id := fmt.Sprintf("R%d", i+1)
		ops = append(ops, op{kind: opDeclare, a: id, n: 1 + rng.Intn(4)})
		resN[id] = 4
	}
	for i := 0; i < MaxTasks+2; i++ {
		ops = append(ops, op{kind: opAddTask, a: fmt.Sprintf("T%d", i+1), n: i + 1, mu: genMu(rng, resN)})
	}
	for i := 0; i < 20; i++ {
		ops = append(ops, genOp(rng, resN))
	}
	return ops
}

// genDirected builds sequences that drive deep stacks and held resources:
// fresh job ids, declared tasks picked by name, and top-of-stack biased
// Acquire/Release/Finish, so ceiling blocks and nesting actually occur.
func genDirected(rng *rand.Rand) []op {
	var ops []op
	nRes := 1 + rng.Intn(2)
	resN := map[string]int{}
	resIDs := make([]string, 0, nRes)
	for i := 0; i < nRes; i++ {
		id := fmt.Sprintf("R%d", i+1)
		n := 2 + rng.Intn(4)
		resN[id] = n
		resIDs = append(resIDs, id)
		ops = append(ops, op{kind: opDeclare, a: id, n: n})
	}
	nTask := 3 + rng.Intn(5)
	taskIDs := make([]string, 0, nTask)
	for i := 0; i < nTask; i++ {
		id := fmt.Sprintf("T%d", i+1)
		taskIDs = append(taskIDs, id)
		ops = append(ops, op{kind: opAddTask, a: id, n: 1 + rng.Intn(10), mu: genMu(rng, resN)})
	}
	var running []string // jobs started and not yet finished
	jobSeq := 0
	steps := 30 + rng.Intn(40)
	for i := 0; i < steps; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // start a fresh job of a declared task
			jobSeq++
			job := fmt.Sprintf("J%d", jobSeq)
			ops = append(ops, op{kind: opStart, a: job, b: taskIDs[rng.Intn(len(taskIDs))]})
			running = append(running, job)
		case 4, 5, 6: // acquire from the most recent job
			if len(running) > 0 {
				ops = append(ops, op{kind: opAcquire,
					a: running[len(running)-1],
					b: resIDs[rng.Intn(len(resIDs))],
					n: 1 + rng.Intn(3)})
			}
		case 7, 8: // release from the most recent job
			if len(running) > 0 {
				ops = append(ops, op{kind: opRelease,
					a: running[len(running)-1],
					b: resIDs[rng.Intn(len(resIDs))],
					n: 1 + rng.Intn(3)})
			}
		default: // finish the most recent job
			if len(running) > 0 {
				ops = append(ops, op{kind: opFinish, a: running[len(running)-1]})
				running = running[:len(running)-1]
			}
		}
	}
	return ops
}

func genMu(rng *rand.Rand, resN map[string]int) []MuEntry {
	var mu []MuEntry
	for _, e := range genMuRaw(rng, resN) {
		mu = append(mu, e)
	}
	return mu
}

func genMuRaw(rng *rand.Rand, resN map[string]int) []MuEntry {
	n := rng.Intn(3) // 0..2 entries
	mu := make([]MuEntry, 0, n)
	ids := make([]string, 0, len(resN))
	for id := range resN {
		ids = append(ids, id)
	}
	for i := 0; i < n; i++ {
		res := "R1"
		if len(ids) > 0 {
			res = ids[rng.Intn(len(ids))]
		}
		if rng.Intn(10) == 0 || len(ids) == 0 {
			res = "ZZ"
		}
		units := 1 + rng.Intn(6)
		if total, ok := resN[res]; ok && rng.Intn(4) > 0 {
			units = 1 + rng.Intn(total) // usually a legal declaration
		}
		mu = append(mu, MuEntry{Resource: res, Units: units})
	}
	// Occasionally duplicate a resource to trigger the invalid-param path.
	if len(mu) == 2 && rng.Intn(4) == 0 {
		mu[1].Resource = mu[0].Resource
	}
	return mu
}

func genOp(rng *rand.Rand, resN map[string]int) op {
	switch rng.Intn(12) {
	case 0:
		return op{kind: opDeclare, a: pickID(rng), n: rng.Intn(7)} // 0 is invalid
	case 1, 2:
		return op{kind: opAddTask, a: pickID(rng), n: rng.Intn(8), mu: genMuRaw(rng, resN)}
	case 3:
		return op{kind: opRemoveTask, a: pickID(rng)}
	case 4, 5, 6:
		return op{kind: opStart, a: pickID(rng), b: pickID(rng)}
	case 7, 8:
		return op{kind: opAcquire, a: pickID(rng), b: pickID(rng), n: rng.Intn(5)}
	case 9, 10:
		return op{kind: opRelease, a: pickID(rng), b: pickID(rng), n: rng.Intn(5)}
	default:
		return op{kind: opFinish, a: pickID(rng)}
	}
}

// --- differential test ---

var reasons = []error{
	ErrInvalidParam, ErrNotFound, ErrDuplicate, ErrCapacity, ErrNotTop,
	ErrLevelInsufficient, ErrCeilingBlocked, ErrExceedsDeclared,
	ErrInsufficientUnits, ErrNotHeld, ErrStillHolding, ErrTaskInUse,
}

// reasonOf maps an error to its rejection reason for comparison.
func reasonOf(err error) error {
	if err == nil {
		return nil
	}
	for _, r := range reasons {
		if errors.Is(err, r) {
			return r
		}
	}
	return fmt.Errorf("unclassified: %w", err)
}

// compareState checks that all pure queries agree on both implementations.
func compareState(t *testing.T, m *Manager, mm *model) {
	t.Helper()
	if got, want := m.SysCeil(), mm.sysCeil(); got != want {
		t.Fatalf("SysCeil: manager=%d model=%d", got, want)
	}
	if got, want := m.Stack(), modelStack(mm); !reflect.DeepEqual(got, want) {
		t.Fatalf("Stack: manager=%v model=%v", got, want)
	}
	if len(m.resOrder) != len(mm.total) {
		t.Fatalf("resource count: manager=%d model=%d", len(m.resOrder), len(mm.total))
	}
	for id := range mm.total {
		avail, err := m.Avail(id)
		if err != nil {
			t.Fatalf("Avail(%q): %v", id, err)
		}
		if avail != mm.avail[id] {
			t.Fatalf("Avail(%q): manager=%d model=%d", id, avail, mm.avail[id])
		}
		ceil, err := m.Ceil(id)
		if err != nil {
			t.Fatalf("Ceil(%q): %v", id, err)
		}
		if ceil != mm.ceil(id) {
			t.Fatalf("Ceil(%q): manager=%d model=%d", id, ceil, mm.ceil(id))
		}
	}
	if len(m.tasks) != len(mm.tasks) {
		t.Fatalf("task count: manager=%d model=%d", len(m.tasks), len(mm.tasks))
	}
	for id := range mm.tasks {
		lv, err := m.Level(id)
		if err != nil {
			t.Fatalf("Level(%q): %v", id, err)
		}
		if lv != mm.level(id) {
			t.Fatalf("Level(%q): manager=%d model=%d", id, lv, mm.level(id))
		}
	}
}

func modelStack(mm *model) []string {
	out := make([]string, len(mm.stack))
	for i, j := range mm.stack {
		out[i] = j.id
	}
	return out
}

// runSequence replays ops on a fresh Manager and returns the reason trace.
func runSequence(ops []op) []error {
	m := NewManager()
	trace := make([]error, len(ops))
	for i, o := range ops {
		trace[i] = reasonOf(applyOp(m, o))
	}
	return trace
}

func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	tally := map[error]int{}
	for seq := 0; seq < 2000; seq++ {
		ops := genSequence(rng)
		t.Run(fmt.Sprintf("seq%04d", seq), func(t *testing.T) {
			m := NewManager()
			mm := newModel()
			for i, o := range ops {
				gotErr := applyOp(m, o)
				wantErr, rationale := applyModel(mm, o)
				got, want := reasonOf(gotErr), reasonOf(wantErr)
				tally[got]++
				t.Logf("op %02d: %-40s -> %v (%s)", i, o, outcome(got), rationale)
				if got != want {
					t.Fatalf("op %d %s: manager reason %v, model reason %v (%s)",
						i, o, got, want, rationale)
				}
				compareState(t, m, mm)
				checkInvariants(t, m)
			}
			// The SRP gate guarantees Acquire never starves: the
			// insufficient-units assertion counter must stay 0.
			if m.insufficientUnitsCount != 0 {
				t.Fatalf("insufficientUnitsCount = %d, want 0", m.insufficientUnitsCount)
			}
			// Replaying the same sequence must reproduce identical results.
			first := runSequence(ops)
			second := runSequence(ops)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("replay mismatch:\nfirst:  %v\nsecond: %v", first, second)
			}
		})
	}
	if t.Failed() {
		return
	}
	// Every rejection reason except ErrInsufficientUnits must have been
	// exercised; ErrInsufficientUnits must never fire at all.
	for _, r := range reasons {
		if r == ErrInsufficientUnits {
			if tally[r] != 0 {
				t.Fatalf("ErrInsufficientUnits fired %d times, want 0", tally[r])
			}
			continue
		}
		if tally[r] == 0 {
			t.Fatalf("rejection reason %v was never exercised", r)
		}
	}
	t.Logf("rejection tally: %v", tally)
}

func outcome(reason error) string {
	if reason == nil {
		return "ok"
	}
	return reason.Error()
}

// TestConcurrent hammers one manager from many goroutines; run with -race.
// It only checks that the manager stays consistent and linearizable enough
// to preserve its invariants under concurrent access.
func TestConcurrent(t *testing.T) {
	m := NewManager()
	mustDeclare(t, m, "R", 8)
	for i := 0; i < 8; i++ {
		mustAddTask(t, m, fmt.Sprintf("T%d", i), 100+i, MuEntry{Resource: "R", Units: 2})
	}
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				job := fmt.Sprintf("g%d-j%d", g, i)
				task := fmt.Sprintf("T%d", rng.Intn(8))
				if err := m.Start(job, task); err == nil {
					_ = m.Acquire(job, "R", 1)
					_ = m.Release(job, "R", 1)
					_ = m.Finish(job)
				}
				_ = m.SysCeil()
				_, _ = m.Ceil("R")
				_, _ = m.Avail("R")
				_ = m.Stack()
				_, _ = m.Level(task)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	checkInvariants(t, m)
	if got := m.insufficientUnitsCount; got != 0 {
		t.Fatalf("insufficientUnitsCount = %d, want 0", got)
	}
}
