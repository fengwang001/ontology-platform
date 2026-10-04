package notify

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"testing"
)

// naive is an independent, literal step-by-step implementation of the
// specification, used to cross-check the real Scheduler.
type naive struct {
	base, capV, m, a, raCap, k, cool int64
	jitter                           Jitter
	tasks                            map[string]*naiveTask
	gfail                            map[string]int64
	openUntil                        map[string]int64
	maxNow                           int64
}

type naiveTask struct {
	chain    []string
	cur      int
	n, att   int64
	nextAt   int64
	deadline int64
	done     bool
}

func newNaive(base, capV, m, a, raCap, k, cool int64, j Jitter) *naive {
	return &naive{
		base: base, capV: capV, m: m, a: a, raCap: raCap, k: k, cool: cool,
		jitter:    j,
		tasks:     map[string]*naiveTask{},
		gfail:     map[string]int64{},
		openUntil: map[string]int64{},
	}
}

func (v *naive) submit(id string, chain []string, created, ttl int64) error {
	if id == "" || len(chain) < 1 || len(chain) > 4 ||
		created < 0 || created > 1_000_000_000_000 || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, ch := range chain {
		if ch == "" || seen[ch] {
			return ErrInvalidArgument
		}
		seen[ch] = true
	}
	if _, dup := v.tasks[id]; dup {
		return ErrDuplicateTask
	}
	v.tasks[id] = &naiveTask{chain: append([]string(nil), chain...), nextAt: created, deadline: created + ttl}
	return nil
}

// check applies the shared rejection order: not found, finished, clock
// regression, too early (argument validation happens before check).
func (v *naive) check(id string, now int64) (*naiveTask, error) {
	tk, ok := v.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if tk.done {
		return nil, ErrTaskFinished
	}
	if now < v.maxNow {
		return nil, ErrClockRegression
	}
	if now < tk.nextAt {
		return nil, ErrTooEarly
	}
	return tk, nil
}

func (v *naive) fail(id string, now int64, class Class, ra int64) (Outcome, error) {
	validClass := class == ClassTransient || class == ClassThrottled || class == ClassPermanent
	if !validClass || ra < 0 || ra > 1_000_000_000 ||
		(validClass && class != ClassThrottled && ra != 0) ||
		now < 0 || now > 1_000_000_000_000 {
		return Outcome{}, ErrInvalidArgument
	}
	tk, err := v.check(id, now)
	if err != nil {
		return Outcome{}, err
	}
	v.maxNow = now

	// Step 1: total failure count.
	tk.att++
	ch := tk.chain[tk.cur]

	// Step 2: global streak and circuit breaker.
	if class != ClassPermanent {
		v.gfail[ch]++
		if v.gfail[ch] >= v.k {
			v.openUntil[ch] = now + v.cool
		}
	}

	// Step 3: switch or same-channel backoff.
	reason := ReasonNone
	var w int64
	sw := false
	if class == ClassPermanent {
		sw, reason = true, ReasonPermanent
	} else if class == ClassThrottled && ra > v.raCap {
		sw, reason = true, ReasonThrottled
	} else {
		tk.n++
		if tk.n >= v.m {
			sw, reason = true, ReasonExhausted
		} else {
			d := v.base << (tk.n - 1)
			if d > v.capV {
				d = v.capV
			}
			j := v.jitter(id, tk.n, d)
			if j < 0 {
				j = 0
			}
			if j > d/4 {
				j = d / 4
			}
			w = d - j
			if ra > w {
				w = ra
			}
		}
	}

	// Step 4: switch branch.
	if sw {
		nxt := -1
		for i := tk.cur + 1; i < len(tk.chain); i++ {
			if v.openUntil[tk.chain[i]] <= now {
				nxt = i
				break
			}
		}
		if nxt < 0 {
			tk.done = true
			return Outcome{Dead: true, Reason: reason}, nil
		}
		tk.cur = nxt
		tk.n = 0
		w = 0
	}

	// Step 5: budget.
	if tk.att >= v.a {
		tk.done = true
		return Outcome{Dead: true, Reason: ReasonBudget}, nil
	}

	// Step 6: schedule or expire.
	tk.nextAt = now + w
	if tk.nextAt > tk.deadline {
		tk.done = true
		return Outcome{Dead: true, Reason: ReasonExpired}, nil
	}
	return Outcome{Channel: tk.chain[tk.cur], NextAt: tk.nextAt}, nil
}

func (v *naive) success(id string, now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	tk, err := v.check(id, now)
	if err != nil {
		return err
	}
	v.maxNow = now
	tk.done = true
	v.gfail[tk.chain[tk.cur]] = 0
	return nil
}

// op is one randomized operation in a fuzz sequence.
type op struct {
	kind    string // "submit", "fail", "success"
	id      string
	chain   []string
	created int64
	ttl     int64
	now     int64
	class   Class
	ra      int64
}

func (o op) String() string {
	switch o.kind {
	case "submit":
		return fmt.Sprintf("Submit(id=%q chain=%v created=%d ttl=%d)", o.id, o.chain, o.created, o.ttl)
	case "fail":
		return fmt.Sprintf("Fail(id=%q now=%d class=%q ra=%d)", o.id, o.now, o.class, o.ra)
	default:
		return fmt.Sprintf("Success(id=%q now=%d)", o.id, o.now)
	}
}

// result captures the observable output of one operation.
type result struct {
	out Outcome
	err error
}

func applyOp(s *Scheduler, o op) result {
	switch o.kind {
	case "submit":
		return result{err: s.Submit(o.id, o.chain, o.created, o.ttl)}
	case "fail":
		out, err := s.Fail(o.id, o.now, o.class, o.ra)
		return result{out: out, err: err}
	default:
		return result{err: s.Success(o.id, o.now)}
	}
}

func applyNaive(v *naive, o op) result {
	switch o.kind {
	case "submit":
		return result{err: v.submit(o.id, o.chain, o.created, o.ttl)}
	case "fail":
		out, err := v.fail(o.id, o.now, o.class, o.ra)
		return result{out: out, err: err}
	default:
		return result{err: v.success(o.id, o.now)}
	}
}

// describe renders the decision basis of an accepted/rejected operation.
func describe(r result) string {
	if r.err != nil {
		return "rejected: " + r.err.Error()
	}
	if r.out.Dead {
		return fmt.Sprintf("dead-letter reason=%q", r.out.Reason)
	}
	if r.out.Channel != "" {
		return fmt.Sprintf("next attempt on %q at %d", r.out.Channel, r.out.NextAt)
	}
	return "ok"
}

var channelPool = []string{"push", "sms", "email", "webhook"}

func hashJitter(seed int64) Jitter {
	return func(id string, n, d int64) int64 {
		h := fnv.New64a()
		fmt.Fprintf(h, "%d|%s|%d|%d", seed, id, n, d)
		// Range [-d, 2d] exercises both clamp bounds and the pass-through.
		return int64(h.Sum64()%(uint64(3*d)+1)) - d
	}
}

func genSequence(rng *rand.Rand, seq int) (params [7]int64, ops []op) {
	base := 1 + rng.Int63n(1000)
	capV := base + rng.Int63n(1000*base)
	if capV > 1_000_000_000 {
		capV = 1_000_000_000
	}
	params = [7]int64{
		base, capV,
		1 + rng.Int63n(6),   // m
		1 + rng.Int63n(10),  // a
		rng.Int63n(201),     // raCap
		1 + rng.Int63n(6),   // k
		1 + rng.Int63n(300), // cool
	}

	var clock int64
	nOps := 10 + rng.Intn(40)
	ids := []string{}
	for i := 0; i < nOps; i++ {
		clock += rng.Int63n(30)
		now := clock
		if rng.Intn(20) == 0 && now > 0 {
			now -= rng.Int63n(now) // provoke clock-regression rejections
		}
		roll := rng.Intn(100)
		switch {
		case roll < 35 || len(ids) == 0:
			id := fmt.Sprintf("s%d-task-%d", seq, len(ids))
			size := 1 + rng.Intn(4)
			perm := rng.Perm(4)[:size]
			chain := make([]string, size)
			for j, p := range perm {
				chain[j] = channelPool[p]
			}
			ops = append(ops, op{kind: "submit", id: id, chain: chain,
				created: clock, ttl: 1 + rng.Int63n(500)})
			ids = append(ids, id)
		case roll < 85:
			id := ids[rng.Intn(len(ids))]
			if rng.Intn(30) == 0 {
				id = "ghost"
			}
			o := op{kind: "fail", id: id, now: now}
			switch rng.Intn(10) {
			case 0, 1, 2:
				o.class = ClassThrottled
				o.ra = rng.Int63n(300)
			case 3, 4:
				o.class = ClassPermanent
			case 5:
				o.class = Class("bogus") // invalid class rejection
			case 6:
				o.class = ClassTransient
				o.ra = int64(1 + rng.Intn(10)) // ra on non-throttled
			default:
				o.class = ClassTransient
			}
			ops = append(ops, o)
		default:
			ops = append(ops, op{kind: "success", id: ids[rng.Intn(len(ids))], now: now})
		}
	}
	return params, ops
}

// TestFuzzAgainstNaive replays 2000 random operation sequences against both
// the real scheduler and the literal naive model, comparing every output,
// checking invariants, and verifying that replays are bit-identical.
func TestFuzzAgainstNaive(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < sequences; seq++ {
		params, ops := genSequence(rng, seq)
		jitter := hashJitter(int64(seq))
		p := params

		real1, err := NewScheduler(p[0], p[1], p[2], p[3], p[4], p[5], p[6], jitter)
		if err != nil {
			t.Fatalf("seq %d: NewScheduler: %v", seq, err)
		}
		real2, _ := NewScheduler(p[0], p[1], p[2], p[3], p[4], p[5], p[6], jitter)
		sim := newNaive(p[0], p[1], p[2], p[3], p[4], p[5], p[6], jitter)

		t.Logf("seq=%d params base=%d cap=%d m=%d a=%d raCap=%d k=%d cool=%d ops=%d",
			seq, p[0], p[1], p[2], p[3], p[4], p[5], p[6], len(ops))

		lastCur := map[string]int{}
		finished := map[string]bool{}
		for i, o := range ops {
			wasFinished := finished[o.id]
			got := applyOp(real1, o)
			want := applyNaive(sim, o)
			replay := applyOp(real2, o)

			t.Logf("  op %d: %s => %s", i, o, describe(got))

			if got.err != want.err || got.out != want.out {
				t.Fatalf("seq %d op %d %s:\n real=%+v err=%v\n naive=%+v err=%v",
					seq, i, o, got.out, got.err, want.out, want.err)
			}
			if !reflect.DeepEqual(got, replay) {
				t.Fatalf("seq %d op %d %s: replay diverged: %+v vs %+v", seq, i, o, got, replay)
			}

			// Invariants on the real scheduler after every operation.
			tk, ok := real1.tasks[o.id]
			if !ok {
				continue
			}
			if tk.att > real1.a {
				t.Fatalf("seq %d op %d: att=%d exceeds A=%d", seq, i, tk.att, real1.a)
			}
			if !tk.done && tk.n >= real1.m {
				t.Fatalf("seq %d op %d: n=%d not below M=%d", seq, i, tk.n, real1.m)
			}
			if tk.cur < lastCur[o.id] {
				t.Fatalf("seq %d op %d: cur decreased", seq, i)
			}
			lastCur[o.id] = tk.cur
			if got.err == nil && wasFinished && o.kind != "submit" {
				t.Fatalf("seq %d op %d: task finished twice", seq, i)
			}
			if tk.done {
				finished[o.id] = true
			}
		}
	}
}

// TestConcurrentSmoke hammers one scheduler from many goroutines; run with
// -race. All operations must be linearizable, so every accepted call must
// still satisfy the invariants and every finished task must reject further
// operations with ErrTaskFinished.
func TestConcurrentSmoke(t *testing.T) {
	jitter := hashJitter(1)
	s, err := NewScheduler(2, 100, 3, 50, 100, 3, 50, jitter)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for g := 0; g < 16; g++ {
		go func(g int) {
			rng := rand.New(rand.NewSource(int64(g)))
			id := fmt.Sprintf("worker-%d", g)
			if err := s.Submit(id, channelPool[:3], 0, 1_000_000); err != nil {
				t.Errorf("submit: %v", err)
			}
			var now int64
			for i := 0; i < 500; i++ {
				now += rng.Int63n(5)
				switch rng.Intn(10) {
				case 0:
					_ = s.Success(id, now)
				default:
					class := ClassTransient
					var ra int64
					switch rng.Intn(3) {
					case 1:
						class = ClassThrottled
						ra = rng.Int63n(150)
					case 2:
						class = ClassPermanent
					}
					_, _ = s.Fail(id, now, class, ra)
				}
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 16; g++ {
		<-done
	}
	for id, tk := range s.tasks {
		if tk.att > s.a || (!tk.done && tk.n >= s.m) {
			t.Errorf("task %s violates invariants: att=%d n=%d", id, tk.att, tk.n)
		}
		if tk.done {
			if _, err := s.Fail(id, s.maxNow, ClassTransient, 0); !errors.Is(err, ErrTaskFinished) {
				t.Errorf("task %s: post-finish Fail = %v", id, err)
			}
		}
	}
}
