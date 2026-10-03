package ontology

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
)

func newTestReviewer(t *testing.T, total int64, percentages []int) *Reviewer {
	t.Helper()
	reviewer, err := NewReviewer(total, percentages, 10, 2, 20, 2, 100, 2, 30, 2)
	if err != nil {
		t.Fatalf("NewReviewer() error = %v", err)
	}
	return reviewer
}

func TestBatchCumulativeDedupAndLastCount(t *testing.T) {
	reviewer := newTestReviewer(t, 10, []int{1, 2, 10, 50, 100})
	got := reviewer.cumulative
	want := []int64{1, 5, 10}
	if len(got) != len(want) {
		t.Fatalf("cumulative = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cumulative = %v, want %v", got, want)
		}
	}
}

func TestWorkedExample(t *testing.T) {
	reviewer, err := NewReviewer(10, []int{10, 50, 100}, 10, 2, 20, 2, 100, 2, 30, 2)
	if err != nil {
		t.Fatalf("NewReviewer() error = %v", err)
	}
	if err := reviewer.Start(0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	steps := []struct {
		name string
		now  int64
		rn   int64
		en   int64
		rb   int64
		eb   int64
		want string
	}{
		{"soak", 5, 100, 1, 450, 4, OutcomeSoaking},
		{"first pass", 10, 100, 1, 450, 5, OutcomePassed},
		{"advance", 11, 0, 0, 0, 0, OutcomeAdvanced},
		{"first failure", 21, 200, 6, 800, 8, OutcomeFailed},
		{"rollback", 22, 0, 0, 0, 0, OutcomeRolledBack},
		{"rollback soak", 60, 50, 0, 50, 0, OutcomeSoaking},
		{"pass after silence", 62, 60, 0, 60, 0, OutcomePassed},
	}
	for _, step := range steps {
		result, err := reviewer.Observe(step.now, step.rn, step.en, step.rb, step.eb)
		if err != nil {
			t.Fatalf("%s: Observe() error = %v", step.name, err)
		}
		t.Logf("step=%s input=(now=%d rn=%d en=%d rb=%d eb=%d) output=%+v status=%+v basis=state-machine transition", step.name, step.now, step.rn, step.en, step.rb, step.eb, result, reviewer.Status())
		if result.Outcome != step.want {
			t.Fatalf("%s: outcome = %q, want %q", step.name, result.Outcome, step.want)
		}
	}

	status := reviewer.Status()
	if status.Batch != 0 || status.Instances != 1 || status.StartTime != 52 || status.RollbackCount != 1 {
		t.Fatalf("status = %+v, want batch 0, instances 1, start 52, rollbacks 1", status)
	}
	if status.NewRequests != 110 || status.NewErrors != 0 || status.BaselineRequests != 110 || status.BaselineErrors != 0 || status.PassStreak != 1 || status.Failures != 0 {
		t.Fatalf("unexpected post-rollback counters: %+v", status)
	}
}

func assertErrorIs(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

type testConfig struct {
	n    int64
	p    []int
	s    int64
	k    int
	tol  int
	ef   int64
	nmin int64
	r    int
	h    int64
	m    int
}

type testOp struct {
	now  int64
	rn   int64
	en   int64
	rb   int64
	eb   int64
	want string
}

func runTransitionCase(t *testing.T, config testConfig, ops []testOp) *Reviewer {
	t.Helper()
	reviewer, err := NewReviewer(config.n, config.p, config.s, config.k, config.tol, config.ef, config.nmin, config.r, config.h, config.m)
	if err != nil {
		t.Fatalf("NewReviewer() error = %v", err)
	}
	if err := reviewer.Start(0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for index, op := range ops {
		result, err := reviewer.Observe(op.now, op.rn, op.en, op.rb, op.eb)
		if err != nil {
			t.Fatalf("op %d: Observe() error = %v", index, err)
		}
		t.Logf("case=%s op=%d input=(now=%d rn=%d en=%d rb=%d eb=%d) output=%+v status=%+v", t.Name(), index, op.now, op.rn, op.en, op.rb, op.eb, result, reviewer.Status())
		if result.Outcome != op.want {
			t.Fatalf("op %d: outcome = %q, want %q", index, result.Outcome, op.want)
		}
	}
	return reviewer
}

func TestGateAndTransitionBoundaries(t *testing.T) {
	base := testConfig{n: 10, p: []int{20, 60, 100}, s: 0, k: 1, tol: 0, ef: 1, nmin: 1, r: 1, h: 30, m: 2}

	t.Run("soak boundary", func(t *testing.T) {
		config := base
		config.s = 10
		reviewer := runTransitionCase(t, config, []testOp{
			{now: 9, rn: 5, en: 0, rb: 5, eb: 0, want: OutcomeSoaking},
			{now: 10, rn: 5, en: 0, rb: 5, eb: 0, want: OutcomeAdvanced},
		})
		if status := reviewer.Status(); status.Batch != 1 || status.StartTime != 10 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("sample boundary", func(t *testing.T) {
		config := base
		config.nmin = 10
		runTransitionCase(t, config, []testOp{
			{now: 0, rn: 9, en: 0, rb: 9, eb: 0, want: OutcomeInsufficientSample},
			{now: 1, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeAdvanced},
		})
	})

	t.Run("error floor boundary", func(t *testing.T) {
		config := base
		config.ef = 5
		config.k = 2
		runTransitionCase(t, config, []testOp{
			{now: 0, rn: 100, en: 4, rb: 100, eb: 100, want: OutcomePassed},
			{now: 1, rn: 0, en: 0, rb: 0, eb: 0, want: OutcomeAdvanced},
		})
	})

	t.Run("inequality equality passes", func(t *testing.T) {
		runTransitionCase(t, base, []testOp{
			{now: 0, rn: 100, en: 2, rb: 100, eb: 2, want: OutcomeAdvanced},
		})
	})

	t.Run("zero baseline requests passes", func(t *testing.T) {
		runTransitionCase(t, base, []testOp{
			{now: 0, rn: 100, en: 10, rb: 0, eb: 0, want: OutcomeAdvanced},
		})
	})

	t.Run("failure clears pass streak", func(t *testing.T) {
		config := base
		config.k = 2
		config.r = 2
		reviewer := runTransitionCase(t, config, []testOp{
			{now: 0, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomePassed},
			{now: 1, rn: 99, en: 1, rb: 99, eb: 0, want: OutcomeFailed},
		})
		if status := reviewer.Status(); status.PassStreak != 0 || status.Failures != 1 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("insufficient sample preserves counts", func(t *testing.T) {
		config := base
		config.nmin = 10
		reviewer := runTransitionCase(t, config, []testOp{
			{now: 0, rn: 9, en: 0, rb: 9, eb: 0, want: OutcomeInsufficientSample},
		})
		if status := reviewer.Status(); status.PassStreak != 0 || status.Failures != 0 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("last batch completes", func(t *testing.T) {
		config := base
		config.p = []int{100}
		reviewer := runTransitionCase(t, config, []testOp{
			{now: 0, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeCompleted},
		})
		status := reviewer.Status()
		if status.State != StateCompleted || status.Instances != config.n {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("rollback restarts silence", func(t *testing.T) {
		reviewer := runTransitionCase(t, base, []testOp{
			{now: 0, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeAdvanced},
			{now: 5, rn: 1, en: 1, rb: 1, eb: 0, want: OutcomeRolledBack},
			{now: 34, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeSoaking},
		})
		status := reviewer.Status()
		if status.Batch != 0 || status.Instances != 2 || status.StartTime != 35 || status.RollbackCount != 1 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("batch zero abort", func(t *testing.T) {
		reviewer := runTransitionCase(t, base, []testOp{
			{now: 0, rn: 1, en: 1, rb: 1, eb: 0, want: OutcomeAborted},
		})
		status := reviewer.Status()
		if status.State != StateAborted || status.Instances != 0 || status.RollbackCount != 1 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("rollback limit freezes", func(t *testing.T) {
		config := base
		config.m = 1
		reviewer := runTransitionCase(t, config, []testOp{
			{now: 0, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeAdvanced},
			{now: 5, rn: 1, en: 1, rb: 1, eb: 0, want: OutcomeFrozen},
		})
		status := reviewer.Status()
		if status.State != StateFrozen || status.Batch != 0 || status.Instances != 2 {
			t.Fatalf("status = %+v", status)
		}
		if _, err := reviewer.Observe(35, 0, 0, 0, 0); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("frozen Observe() error = %v, want %v", err, ErrInvalidState)
		}
	})
}

func TestRejectionPriorityLeavesStateUntouched(t *testing.T) {
	reviewer := newTestReviewer(t, 10, []int{10, 100})
	if err := reviewer.Start(1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	before := reviewer.Status()
	if err := reviewer.Start(2); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second Start() error = %v, want %v", err, ErrInvalidState)
	}
	if _, err := reviewer.Observe(-1, 0, 1, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Observe() error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := reviewer.Observe(-1, 0, 0, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument has priority over clock rollback: %v", err)
	}
	if _, err := reviewer.Observe(0, 1, 2, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("errors greater than requests error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := reviewer.Observe(0, 0, 0, 0, 0); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("clock rollback error = %v, want %v", err, ErrClockRolledBack)
	}

	frozen := runTransitionCase(t, testConfig{n: 10, p: []int{20, 100}, s: 0, k: 1, tol: 0, ef: 1, nmin: 1, r: 1, h: 0, m: 1}, []testOp{
		{now: 0, rn: 1, en: 0, rb: 1, eb: 0, want: OutcomeAdvanced},
		{now: 1, rn: 1, en: 1, rb: 1, eb: 0, want: OutcomeFrozen},
	})
	if _, err := frozen.Observe(-1, 0, 0, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("frozen invalid timestamp error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := frozen.Observe(0, 0, 0, 0, 0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("frozen valid timestamp error = %v, want %v", err, ErrInvalidState)
	}

	status := reviewer.Status()
	if reviewer.maxNow != 1 || status.State != StateRunning || status.StartTime != 1 {
		t.Fatalf("unexpected status after rejections: %+v", status)
	}
	if !reflect.DeepEqual(status, before) {
		t.Fatalf("rejected operation changed status: before=%+v after=%+v", before, status)
	}
}

func TestLargeGateProductsAreExact(t *testing.T) {
	config := testConfig{n: 10, p: []int{100}, s: 0, k: 1, tol: 0, ef: 1, nmin: 1_000_000_000, r: 1, h: 0, m: 1}

	reviewer := runTransitionCase(t, config, []testOp{
		{now: 0, rn: 1_000_000_000, en: 999_999_999, rb: 1_000_000_000, eb: 999_999_999, want: OutcomeCompleted},
	})
	if status := reviewer.Status(); status.State != StateCompleted {
		t.Fatalf("status = %+v", status)
	}

	failureReviewer, err := NewReviewer(config.n, config.p, config.s, config.k, config.tol, config.ef, config.nmin, config.r, config.h, config.m)
	if err != nil {
		t.Fatalf("NewReviewer() error = %v", err)
	}
	if err := failureReviewer.Start(0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := failureReviewer.Observe(0, 1_000_000_000, 1_000_000_000, 1_000_000_000, 999_999_999)
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if result.Outcome != OutcomeAborted {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeAborted)
	}
}

func TestConstructorAndCumulativeValidation(t *testing.T) {
	invalidConfigs := []testConfig{
		{n: 0, p: []int{100}},
		{n: 1_000_001, p: []int{100}},
		{n: 10, p: nil},
		{n: 10, p: []int{0}},
		{n: 10, p: []int{101}},
		{n: 10, p: []int{50, 40, 100}},
		{n: 10, p: []int{50, 99}},
		{n: 10, p: []int{100}, s: -1},
		{n: 10, p: []int{100}, k: 0},
		{n: 10, p: []int{100}, k: 101},
		{n: 10, p: []int{100}, tol: -1},
		{n: 10, p: []int{100}, tol: 1001},
		{n: 10, p: []int{100}, ef: -1},
		{n: 10, p: []int{100}, nmin: -1},
		{n: 10, p: []int{100}, r: 0},
		{n: 10, p: []int{100}, h: -1},
		{n: 10, p: []int{100}, m: 0},
	}
	for index, config := range invalidConfigs {
		if _, err := NewReviewer(config.n, config.p, config.s, config.k, config.tol, config.ef, config.nmin, config.r, config.h, config.m); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid config %d (%+v): error = %v, want %v", index, config, err, ErrInvalidArgument)
		}
	}

	reviewer := newTestReviewer(t, 10, []int{100})
	if err := reviewer.Start(0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	reviewer.newRequests = 1_000_000_000
	reviewer.baseRequests = 1_000_000_000
	before := reviewer.Status()
	if _, err := reviewer.Observe(0, 1, 0, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cumulative overflow error = %v, want %v", err, ErrInvalidArgument)
	}
	if !reflect.DeepEqual(reviewer.Status(), before) {
		t.Fatalf("overflow rejection changed status: before=%+v after=%+v", before, reviewer.Status())
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	reviewer, err := NewReviewer(100, []int{100}, 0, 5, 1000, 1, 1, 100, 0, 100)
	if err != nil {
		t.Fatalf("NewReviewer() error = %v", err)
	}
	if err := reviewer.Start(0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 50; i++ {
				_, _ = reviewer.Observe(1, 1, 0, 1, 0)
				_ = reviewer.Status()
			}
		}(worker)
	}
	wait.Wait()

	status := reviewer.Status()
	if status.State != StateCompleted || status.Instances != 100 {
		t.Fatalf("status = %+v, want completed with 100 instances", status)
	}
	if status.PassStreak != 5 || status.NewRequests != 5 {
		t.Fatalf("PassStreak = %d, want 5", status.PassStreak)
	}
}

type naiveReviewer struct {
	n     int64
	cum   []int64
	s     int64
	k     int
	tol   int
	ef    int64
	nmin  int64
	r     int
	h     int64
	m     int
	state State
	batch int
	st    int64
	rn    int64
	en    int64
	rb    int64
	eb    int64
	ps    int
	fs    int
	rbk   int
	max   int64
}

func newNaive(config testConfig) *naiveReviewer {
	cumulative := make([]int64, 0, len(config.p))
	for _, percentage := range config.p {
		count := ceilDiv(config.n*int64(percentage), 100)
		if len(cumulative) == 0 || count != cumulative[len(cumulative)-1] {
			cumulative = append(cumulative, count)
		}
	}
	return &naiveReviewer{
		n:    config.n,
		cum:  cumulative,
		s:    config.s,
		k:    config.k,
		tol:  config.tol,
		ef:   config.ef,
		nmin: config.nmin,
		r:    config.r,
		h:    config.h,
		m:    config.m,
	}
}

func (n *naiveReviewer) start(now int64) error {
	if n.state != StateNotStarted {
		return ErrInvalidState
	}
	if now < n.max {
		return ErrClockRolledBack
	}
	n.state = StateRunning
	n.batch = 0
	n.st = now
	n.max = now
	return nil
}

func (n *naiveReviewer) observe(now, rn, en, rb, eb int64) (ObserveResult, error) {
	if n.state != StateRunning {
		return ObserveResult{}, ErrInvalidState
	}
	if now < n.max {
		return ObserveResult{}, ErrClockRolledBack
	}
	n.rn += rn
	n.en += en
	n.rb += rb
	n.eb += eb
	n.max = now

	if now < n.st+n.s {
		return ObserveResult{Accepted: true, Outcome: OutcomeSoaking}, nil
	}
	if n.rn < n.nmin {
		return ObserveResult{Accepted: true, Outcome: OutcomeInsufficientSample}, nil
	}

	left := new(big.Int).Mul(big.NewInt(n.en), big.NewInt(n.rb))
	left.Mul(left, big.NewInt(100))
	right := new(big.Int).Mul(big.NewInt(n.eb), big.NewInt(n.rn))
	right.Mul(right, big.NewInt(int64(100+n.tol)))
	fails := n.en >= n.ef && left.Cmp(right) > 0

	if !fails {
		n.ps++
		if n.ps < n.k {
			return ObserveResult{Accepted: true, Outcome: OutcomePassed}, nil
		}
		if n.batch == len(n.cum)-1 {
			n.state = StateCompleted
			return ObserveResult{Accepted: true, Outcome: OutcomeCompleted}, nil
		}
		n.batch++
		n.st = now
		n.rn, n.en, n.rb, n.eb, n.ps, n.fs = 0, 0, 0, 0, 0, 0
		return ObserveResult{Accepted: true, Outcome: OutcomeAdvanced}, nil
	}

	n.fs++
	n.ps = 0
	if n.fs < n.r {
		return ObserveResult{Accepted: true, Outcome: OutcomeFailed}, nil
	}
	n.rbk++
	if n.batch == 0 {
		n.state = StateAborted
		return ObserveResult{Accepted: true, Outcome: OutcomeAborted}, nil
	}
	n.batch--
	n.st = now + n.h
	n.rn, n.en, n.rb, n.eb, n.ps, n.fs = 0, 0, 0, 0, 0, 0
	if n.rbk >= n.m {
		n.state = StateFrozen
		return ObserveResult{Accepted: true, Outcome: OutcomeFrozen}, nil
	}
	return ObserveResult{Accepted: true, Outcome: OutcomeRolledBack}, nil
}

func (n *naiveReviewer) status() Status {
	instances := int64(0)
	if n.state == StateCompleted {
		instances = n.n
	} else if n.state == StateRunning || n.state == StateFrozen {
		instances = n.cum[n.batch]
	}
	return Status{
		State:            n.state,
		Batch:            n.batch,
		Instances:        instances,
		StartTime:        n.st,
		NewRequests:      n.rn,
		NewErrors:        n.en,
		BaselineRequests: n.rb,
		BaselineErrors:   n.eb,
		PassStreak:       n.ps,
		Failures:         n.fs,
		RollbackCount:    n.rbk,
	}
}

type randomOperation struct {
	now int64
	rn  int64
	en  int64
	rb  int64
	eb  int64
}

func randomConfig(rng *rand.Rand) testConfig {
	n := int64(rng.IntN(40) + 1)
	percentageCount := rng.IntN(4) + 1
	percentages := make([]int, 0, percentageCount)
	last := 0
	for i := 0; i < percentageCount; i++ {
		remainingSlots := percentageCount - i
		remainingValues := 100 - last
		upper := last + remainingValues/remainingSlots
		if i == percentageCount-1 {
			last = 100
		} else if upper <= last {
			last++
		} else {
			last += rng.IntN(upper-last) + 1
		}
		percentages = append(percentages, last)
	}
	return testConfig{
		n:    n,
		p:    percentages,
		s:    int64(rng.IntN(5)),
		k:    rng.IntN(3) + 1,
		tol:  rng.IntN(121),
		ef:   int64(rng.IntN(5)),
		nmin: int64(rng.IntN(121)),
		r:    rng.IntN(3) + 1,
		h:    int64(rng.IntN(5)),
		m:    rng.IntN(3) + 1,
	}
}

func randomPair(rng *rand.Rand) (int64, int64) {
	requests := int64(rng.IntN(10))
	errors := int64(rng.IntN(int(requests) + 1))
	return requests, errors
}

func randomOperations(rng *rand.Rand, config testConfig) []randomOperation {
	count := rng.IntN(80) + 1
	operations := make([]randomOperation, count)
	now := int64(0)
	for i := range operations {
		if i > 0 {
			now += int64(rng.IntN(4))
		}
		op := randomOperation{now: now}
		mode := rng.IntN(5)
		if mode == 0 {
			op.rn, op.en = randomPair(rng)
			op.rb, op.eb = randomPair(rng)
		} else if mode == 1 {
			op.rn, op.en = int64(rng.IntN(100)+1), 0
			op.rb, op.eb = int64(rng.IntN(100)+1), 0
		} else if mode == 2 {
			op.rn, op.en = int64(rng.IntN(10)+1), 0
			op.rb, op.eb = int64(rng.IntN(10)+1), 0
		} else {
			op.rn, op.en = int64(rng.IntN(10)+1), 1
			op.rb, op.eb = int64(rng.IntN(10)+1), 0
		}
		operations[i] = op
	}
	return operations
}

func TestRandomOperationsAgainstNaive(t *testing.T) {
	for seed := uint64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed_%04d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewChaCha8([32]byte{byte(seed), byte(seed >> 8), byte(seed >> 16), byte(seed >> 24)}))
			config := randomConfig(rng)
			reviewer, err := NewReviewer(config.n, config.p, config.s, config.k, config.tol, config.ef, config.nmin, config.r, config.h, config.m)
			if err != nil {
				t.Fatalf("config=%+v NewReviewer() error = %v", config, err)
			}
			naive := newNaive(config)
			if err := reviewer.Start(0); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if err := naive.start(0); err != nil {
				t.Fatalf("naive start() error = %v", err)
			}

			for index, op := range randomOperations(rng, config) {
				result, resultErr := reviewer.Observe(op.now, op.rn, op.en, op.rb, op.eb)
				naiveResult, naiveErr := naive.observe(op.now, op.rn, op.en, op.rb, op.eb)
				t.Logf("seed=%d config=%+v op=%d input=(now=%d rn=%d en=%d rb=%d eb=%d) actual=(result=%+v error=%v status=%+v) naive=(result=%+v error=%v status=%+v) basis=soak then Nmin then exact big.Int inequality then streak/failure transitions", seed, config, index, op.now, op.rn, op.en, op.rb, op.eb, result, resultErr, reviewer.Status(), naiveResult, naiveErr, naive.status())
				if !errors.Is(resultErr, naiveErr) || result.Outcome != naiveResult.Outcome {
					t.Fatalf("result mismatch: actual=(%+v,%v) naive=(%+v,%v)", result, resultErr, naiveResult, naiveErr)
				}
				if !reflect.DeepEqual(reviewer.Status(), naive.status()) {
					t.Fatalf("status mismatch: actual=%+v naive=%+v", reviewer.Status(), naive.status())
				}
			}
		})
	}
}
