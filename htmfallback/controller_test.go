package htmfallback

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

type opKind int

const (
	opLock opKind = iota
	opAccess
	opUnlock
)

type testOp struct {
	kind    opKind
	thread  int
	address int
	isWrite bool
}

type testConfig struct {
	n, s, w, a, r, sk, f int
}

type naiveController struct {
	cfg testConfig

	states  []ThreadState
	reasons []AbortReason
	retries []int
	reads   []map[int]bool
	writes  []map[int]bool

	holder int
	skip   int
	fb     int
}

func newNaive(cfg testConfig) *naiveController {
	model := &naiveController{
		cfg:     cfg,
		states:  make([]ThreadState, cfg.n),
		reasons: make([]AbortReason, cfg.n),
		retries: make([]int, cfg.n),
		reads:   make([]map[int]bool, cfg.n),
		writes:  make([]map[int]bool, cfg.n),
		holder:  -1,
	}
	for index := range model.reads {
		model.reads[index] = make(map[int]bool)
		model.writes[index] = make(map[int]bool)
	}
	return model
}

func (m *naiveController) counts(thread int) ResultCounts {
	return ResultCounts{
		Retries:              m.retries[thread],
		Skip:                 m.skip,
		ConsecutiveFallbacks: m.fb,
	}
}

func (m *naiveController) clear(thread int) {
	clear(m.reads[thread])
	clear(m.writes[thread])
}

func (m *naiveController) lock(thread int) (LockResult, string, error) {
	if thread < 0 || thread >= m.cfg.n {
		return LockResult{}, "invalid-thread", ErrInvalidThread
	}
	if m.states[thread] != StateIdle && m.states[thread] != StateAborted {
		return LockResult{}, "invalid-state", ErrInvalidState
	}

	result := LockResult{
		Thread:       thread,
		State:        m.states[thread],
		Reason:       m.reasons[thread],
		ResultCounts: m.counts(thread),
	}
	if m.holder >= 0 {
		result.Waited = true
		return result, "holder-present-wait", nil
	}

	fallbackPath := ""
	if m.states[thread] == StateAborted && m.reasons[thread] == ReasonCapacity {
		fallbackPath = "capacity-abort-fallback"
	} else if m.skip > 0 {
		m.skip--
		fallbackPath = "remaining-skip-fallback"
	} else if m.retries[thread] > m.cfg.r {
		fallbackPath = "retry-budget-fallback"
	} else {
		m.states[thread] = StateSpeculative
		m.reasons[thread] = ReasonNone
		m.clear(thread)
		result.State = StateSpeculative
		result.Reason = ReasonNone
		result.ResultCounts = m.counts(thread)
		return result, "begin-speculation", nil
	}

	m.holder = thread
	m.states[thread] = StateFallback
	m.reasons[thread] = ReasonNone
	m.clear(thread)

	for index := range m.states {
		if m.states[index] == StateSpeculative {
			m.states[index] = StateAborted
			m.reasons[index] = ReasonLockHeld
			m.clear(index)
			result.Aborted = append(result.Aborted, index)
		}
	}

	if fallbackPath != "remaining-skip-fallback" {
		m.fb++
		if m.fb == m.cfg.f {
			m.skip = m.cfg.sk
			m.fb = 0
		}
	}

	result.State = StateFallback
	result.ResultCounts = m.counts(thread)
	return result, fallbackPath, nil
}

func (m *naiveController) access(thread, address int, isWrite bool) (AccessResult, string, error) {
	if thread < 0 || thread >= m.cfg.n {
		return AccessResult{}, "invalid-thread", ErrInvalidThread
	}
	if m.states[thread] != StateSpeculative && m.states[thread] != StateFallback {
		return AccessResult{}, "invalid-state", ErrInvalidState
	}
	if address < 0 || address >= m.cfg.a {
		return AccessResult{}, "invalid-address", ErrInvalidAddress
	}

	result := AccessResult{
		Thread:       thread,
		Address:      address,
		IsWrite:      isWrite,
		State:        m.states[thread],
		ResultCounts: m.counts(thread),
	}
	if m.states[thread] == StateFallback {
		return result, "fallback-access-ignored", nil
	}

	for index := range m.states {
		if index == thread || m.states[index] != StateSpeculative {
			continue
		}
		conflict := false
		if isWrite && (m.reads[index][address] || m.writes[index][address]) {
			conflict = true
		}
		if !isWrite && m.writes[index][address] {
			conflict = true
		}
		if conflict {
			m.states[index] = StateAborted
			m.reasons[index] = ReasonConflict
			m.retries[index]++
			m.clear(index)
			result.Aborted = append(result.Aborted, index)
		}
	}

	if isWrite {
		m.writes[thread][address] = true
	} else {
		m.reads[thread][address] = true
	}

	group := address % m.cfg.s
	distinct := 0
	for candidate := range m.reads[thread] {
		if candidate%m.cfg.s == group {
			distinct++
		}
	}
	for candidate := range m.writes[thread] {
		if candidate%m.cfg.s == group && !m.reads[thread][candidate] {
			distinct++
		}
	}

	if distinct > m.cfg.w {
		m.states[thread] = StateAborted
		m.reasons[thread] = ReasonCapacity
		m.clear(thread)
		m.skip = m.cfg.sk
		result.State = StateAborted
		result.Reason = ReasonCapacity
		result.ResultCounts = m.counts(thread)
		return result, fmt.Sprintf("conflicts=%v-then-capacity", result.Aborted), nil
	}

	result.State = StateSpeculative
	result.ResultCounts = m.counts(thread)
	return result, fmt.Sprintf("conflicts=%v-speculation-continues", result.Aborted), nil
}

func (m *naiveController) unlock(thread int) (UnlockResult, string, error) {
	if thread < 0 || thread >= m.cfg.n {
		return UnlockResult{}, "invalid-thread", ErrInvalidThread
	}
	if m.states[thread] != StateSpeculative && m.states[thread] != StateFallback {
		return UnlockResult{}, "invalid-state", ErrInvalidState
	}

	speculative := m.states[thread] == StateSpeculative
	if !speculative {
		m.holder = -1
	}
	m.states[thread] = StateIdle
	m.reasons[thread] = ReasonNone
	m.retries[thread] = 0
	m.clear(thread)
	if speculative {
		m.fb = 0
		return UnlockResult{Thread: thread, State: StateIdle, ResultCounts: m.counts(thread)}, "speculative-commit-clear-fb", nil
	}
	return UnlockResult{Thread: thread, State: StateIdle, ResultCounts: m.counts(thread)}, "fallback-unlock-retain-fb", nil
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed+2000)))
		cfg := testConfig{
			n:  1 + rng.IntN(16),
			s:  1 + rng.IntN(8),
			w:  1 + rng.IntN(4),
			a:  1 + rng.IntN(64),
			r:  rng.IntN(9),
			sk: rng.IntN(9),
			f:  1 + rng.IntN(8),
		}
		t.Run(fmt.Sprintf("seed=%d/cfg=%+v", seed, cfg), func(t *testing.T) {
			controller, err := New(cfg.n, cfg.s, cfg.w, cfg.a, cfg.r, cfg.sk, cfg.f)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaive(cfg)

			for step := 0; step < 36; step++ {
				op := testOp{
					kind:   opKind(rng.IntN(3)),
					thread: rng.IntN(cfg.n + 1),
				}
				if op.kind == opAccess {
					op.address = rng.IntN(cfg.a + 1)
					op.isWrite = rng.IntN(2) == 0
				}

				var basis string
				var output any
				switch op.kind {
				case opLock:
					got, gotErr := controller.Lock(op.thread)
					want, wantBasis, wantErr := model.lock(op.thread)
					basis = wantBasis
					output = struct {
						Result LockResult
						Error  error
					}{got, gotErr}
					if !sameError(gotErr, wantErr) || !reflect.DeepEqual(normalizeLock(got), normalizeLock(want)) {
						t.Fatalf("Lock mismatch\ninput=%+v\ngot=(%+v,%v)\nwant=(%+v,%v)\nbasis=%s\ngot snapshot=%+v\nwant snapshot=%+v", op, got, gotErr, want, wantErr, basis, controller.Snapshot(), model.snapshot())
					}
				case opAccess:
					got, gotErr := controller.Access(op.thread, op.address, op.isWrite)
					want, wantBasis, wantErr := model.access(op.thread, op.address, op.isWrite)
					basis = wantBasis
					output = struct {
						Result AccessResult
						Error  error
					}{got, gotErr}
					if !sameError(gotErr, wantErr) || !reflect.DeepEqual(normalizeAccess(got), normalizeAccess(want)) {
						t.Fatalf("Access mismatch\ninput=%+v\ngot=(%+v,%v)\nwant=(%+v,%v)\nbasis=%s\ngot snapshot=%+v\nwant snapshot=%+v", op, got, gotErr, want, wantErr, basis, controller.Snapshot(), model.snapshot())
					}
				case opUnlock:
					got, gotErr := controller.Unlock(op.thread)
					want, wantBasis, wantErr := model.unlock(op.thread)
					basis = wantBasis
					output = struct {
						Result UnlockResult
						Error  error
					}{got, gotErr}
					if !sameError(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
						t.Fatalf("Unlock mismatch\ninput=%+v\ngot=(%+v,%v)\nwant=(%+v,%v)\nbasis=%s\ngot snapshot=%+v\nwant snapshot=%+v", op, got, gotErr, want, wantErr, basis, controller.Snapshot(), model.snapshot())
					}
				}

				gotSnapshot := controller.Snapshot()
				wantSnapshot := model.snapshot()
				if !reflect.DeepEqual(normalizeSnapshot(gotSnapshot), normalizeSnapshot(wantSnapshot)) {
					t.Fatalf("snapshot mismatch after %+v", op)
				}
				t.Logf("seed=%d step=%d input=%+v output=%+v output-snapshot=%+v basis=%s", seed, step, op, output, wantSnapshot, basis)
			}
		})
	}
}

func normalizeLock(value LockResult) LockResult {
	value.Aborted = normalizeInts(value.Aborted)
	return value
}

func normalizeAccess(value AccessResult) AccessResult {
	value.Aborted = normalizeInts(value.Aborted)
	return value
}

func normalizeInts(value []int) []int {
	if value == nil {
		return []int{}
	}
	return value
}

func normalizeSnapshot(value Snapshot) Snapshot {
	for index := range value.Threads {
		value.Threads[index].ReadSet = normalizeInts(value.Threads[index].ReadSet)
		value.Threads[index].WriteSet = normalizeInts(value.Threads[index].WriteSet)
	}
	return value
}

func sameError(got, want error) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func (m *naiveController) snapshot() Snapshot {
	snapshot := Snapshot{
		Threads:              make([]ThreadSnapshot, m.cfg.n),
		Skip:                 m.skip,
		ConsecutiveFallbacks: m.fb,
	}
	if m.holder >= 0 {
		holder := m.holder
		snapshot.Holder = &holder
	}
	for index := range m.states {
		snapshot.Threads[index] = ThreadSnapshot{
			State:    m.states[index],
			Reason:   m.reasons[index],
			Retries:  m.retries[index],
			ReadSet:  sortedBools(m.reads[index]),
			WriteSet: sortedBools(m.writes[index]),
		}
	}
	return snapshot
}

func sortedBools(set map[int]bool) []int {
	values := make([]int, 0, len(set))
	for key, present := range set {
		if present {
			values = append(values, key)
		}
	}
	slices.Sort(values)
	return values
}
