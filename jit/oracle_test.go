package jit

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

type oracleOp struct {
	kind      string
	method    int64
	backEdges int64
	now       int64
}

type oracleMethod struct {
	tier      int
	calls     Counter
	backEdges Counter
	deopts    int64
	epoch     int64
	coolUntil int64
	inFlight  bool
}

type oracle struct {
	cfg      Config
	methods  []oracleMethod
	queue    []Job
	last     int64
	now      int64
	discard  int64
	decision string
}

func newOracle(cfg Config) *oracle {
	return &oracle{cfg: cfg, methods: make([]oracleMethod, cfg.Methods)}
}

func (o *oracle) valid(method, backEdges, now int64) error {
	if method < 0 || method >= o.cfg.Methods || backEdges < 0 || backEdges > 1000000 ||
		now < 0 || now > 1000000000000000 {
		return ErrInvalidArgument
	}
	return nil
}

func (o *oracle) install(now int64) int {
	ready := 0
	for _, job := range o.queue {
		if job.Finish > now {
			break
		}
		ready++
	}
	for idx := 0; idx < ready; idx++ {
		job := o.queue[idx]
		o.methods[job.Method].tier = job.Target
		o.methods[job.Method].inFlight = false
	}
	o.queue = append([]Job(nil), o.queue[ready:]...)
	return ready
}

func (o *oracle) decay(entry *oracleMethod, now int64) {
	nextEpoch := now / o.cfg.DecayPeriod
	gap := nextEpoch - entry.epoch
	if gap <= 0 {
		return
	}
	if gap > 62 {
		gap = 62
	}
	for bit := int64(0); bit < gap; bit++ {
		entry.calls = entry.calls.shiftRight(1)
		entry.backEdges = entry.backEdges.shiftRight(1)
	}
	entry.epoch = nextEpoch
}

func (o *oracle) installedTier(method, now int64) int {
	tier := o.methods[method].tier
	for _, job := range o.queue {
		if job.Finish > now {
			break
		}
		if job.Method == method {
			tier = job.Target
		}
	}
	return tier
}

func (o *oracle) promote(method, now int64) {
	entry := &o.methods[method]
	o.decision = "no-promotion"
	if entry.tier >= 2 || entry.inFlight || now < entry.coolUntil {
		return
	}

	q := int64(len(o.queue))
	scale := 1 + q/o.cfg.FeedbackDivisor
	deoptFactor := 1 + entry.deopts
	calls := entry.calls
	total := calls.add(entry.backEdges)
	h2 := entry.deopts < o.cfg.Tier2DeoptThreshold &&
		(calls.atLeast(multiply3(o.cfg.Tier2Calls, deoptFactor, scale)) ||
			(calls.atLeast(multiply3(o.cfg.Tier2MinCalls, deoptFactor, scale)) &&
				total.atLeast(multiply3(o.cfg.Tier2Total, deoptFactor, scale))))
	h1 := calls.atLeast(multiply64(o.cfg.Tier1Calls, scale)) ||
		(calls.atLeast(multiply64(o.cfg.Tier1MinCalls, scale)) &&
			total.atLeast(multiply64(o.cfg.Tier1Total, scale)))

	target := 0
	if entry.tier == 0 && h2 {
		target = 2
	} else if entry.tier == 0 && h1 {
		target = 1
	} else if entry.tier == 1 && h2 {
		target = 2
	}
	if target == 0 {
		return
	}
	if q >= o.cfg.QueueCapacity {
		o.discard++
		o.decision = fmt.Sprintf("discard target=%d q=%d s=%d d=%d", target, q, scale, deoptFactor)
		return
	}

	start := now
	if o.last > start {
		start = o.last
	}
	duration := o.cfg.Tier1CompileTime
	if target == 2 {
		duration = o.cfg.Tier2CompileTime
	}
	finish := start + duration
	o.queue = append(o.queue, Job{Method: method, Target: target, Start: start, Finish: finish})
	entry.inFlight = true
	o.last = finish
	o.decision = fmt.Sprintf("enqueue target=%d q=%d s=%d d=%d start=%d finish=%d", target, q, scale, deoptFactor, start, finish)
}

func (o *oracle) call(method, backEdges, now int64) (int, error) {
	if err := o.valid(method, backEdges, now); err != nil {
		return 0, err
	}
	if now < o.now {
		return 0, ErrClockRewound
	}
	o.install(now)
	entry := &o.methods[method]
	o.decay(entry, now)
	tier := entry.tier
	entry.calls = entry.calls.add(counterFromInt64(1))
	entry.backEdges = entry.backEdges.add(counterFromInt64(backEdges))
	o.promote(method, now)
	o.now = now
	return tier, nil
}

func (o *oracle) deopt(method, now int64) error {
	if err := o.valid(method, 0, now); err != nil {
		return err
	}
	if now < o.now {
		return ErrClockRewound
	}
	if o.installedTier(method, now) != 2 {
		return ErrNotTier2
	}
	o.install(now)
	entry := &o.methods[method]
	o.decay(entry, now)
	entry.tier = 0
	entry.calls = Counter{}
	entry.backEdges = Counter{}
	entry.deopts++
	entry.coolUntil = now + o.cfg.DeoptCooldownBase*entry.deopts
	o.now = now
	return nil
}

func (o *oracle) state(method, now int64) (Snapshot, error) {
	if err := o.valid(method, 0, now); err != nil {
		return Snapshot{}, err
	}
	if now < o.now {
		return Snapshot{}, ErrClockRewound
	}
	entry := o.methods[method]
	ready := 0
	for _, job := range o.queue {
		if job.Finish > now {
			break
		}
		ready++
	}
	o.decay(&entry, now)
	tier := entry.tier
	for idx := 0; idx < ready; idx++ {
		if o.queue[idx].Method == method {
			tier = o.queue[idx].Target
		}
	}
	return Snapshot{
		MethodState: MethodState{
			Tier:      tier,
			Calls:     entry.calls,
			BackEdges: entry.backEdges,
			Deopts:    entry.deopts,
			CoolUntil: entry.coolUntil,
		},
		QueuedJobs: len(o.queue) - ready,
		LastFinish: o.last,
	}, nil
}

func randomOracleConfig(random *rand.Rand) Config {
	cfg := Config{
		Methods:             int64(random.IntN(8) + 1),
		Tier1Calls:          int64(random.IntN(12) + 1),
		Tier1MinCalls:       int64(random.IntN(12) + 1),
		Tier1Total:          int64(random.IntN(30) + 1),
		Tier2Calls:          int64(random.IntN(24) + 1),
		Tier2MinCalls:       int64(random.IntN(24) + 1),
		Tier2Total:          int64(random.IntN(60) + 1),
		FeedbackDivisor:     int64(random.IntN(4) + 1),
		QueueCapacity:       int64(random.IntN(5) + 1),
		Tier1CompileTime:    int64(random.IntN(6) + 1),
		Tier2CompileTime:    int64(random.IntN(10) + 1),
		DecayPeriod:         int64(random.IntN(20) + 1),
		DeoptCooldownBase:   int64(random.IntN(12) + 1),
		Tier2DeoptThreshold: int64(random.IntN(3) + 1),
	}
	if cfg.Tier1MinCalls > cfg.Tier1Calls {
		cfg.Tier1MinCalls = cfg.Tier1Calls
	}
	if cfg.Tier2MinCalls > cfg.Tier2Calls {
		cfg.Tier2MinCalls = cfg.Tier2Calls
	}
	return cfg
}

func TestRandomOracleComparison(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		seed := uint64(trial + 1)
		random := rand.New(rand.NewPCG(seed, seed+999))
		cfg := randomOracleConfig(random)
		manager, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("trial=%d NewManager() error=%v cfg=%+v", trial, err, cfg)
		}
		model := newOracle(cfg)

		var now int64
		for step := 0; step < 90; step++ {
			now += int64(random.IntN(8))
			method := int64(random.IntN(int(cfg.Methods)))
			op := oracleOp{method: method, now: now, backEdges: int64(random.IntN(16))}
			switch roll := random.IntN(100); {
			case roll < 68:
				op.kind = "call"
			case roll < 82:
				op.kind = "deopt"
			case roll < 94:
				op.kind = "state"
			case roll < 97:
				op.kind = "invalid"
				if random.IntN(2) == 0 {
					op.method = cfg.Methods
				} else {
					op.backEdges = 1000001
				}
			default:
				op.kind = "rewind"
				op.now = -1
			}

			logStep := trial < 2
			if logStep {
				t.Logf("trial=%d step=%d input=%s(m=%d,n=%d,now=%d)", trial, step, op.kind, op.method, op.backEdges, op.now)
			}

			switch op.kind {
			case "call":
				actualTier, actualErr := manager.Call(op.method, op.backEdges, op.now)
				modelTier, modelErr := model.call(op.method, op.backEdges, op.now)
				if actualTier != modelTier || !sameError(actualErr, modelErr) {
					t.Fatalf("call mismatch trial=%d step=%d actual=(%d,%v) model=(%d,%v)", trial, step, actualTier, actualErr, modelTier, modelErr)
				}
			case "deopt":
				actualErr := manager.Deopt(op.method, op.now)
				modelErr := model.deopt(op.method, op.now)
				if !sameError(actualErr, modelErr) {
					t.Fatalf("deopt mismatch trial=%d step=%d actual=%v model=%v", trial, step, actualErr, modelErr)
				}
			case "state":
				actualSnapshot, actualErr := manager.State(op.method, op.now)
				modelSnapshot, modelErr := model.state(op.method, op.now)
				if !sameError(actualErr, modelErr) || actualSnapshot != modelSnapshot {
					t.Fatalf("state mismatch trial=%d step=%d actual=%+v,%v model=%+v,%v", trial, step, actualSnapshot, actualErr, modelSnapshot, modelErr)
				}
			default:
				_, actualErr := manager.Call(op.method, op.backEdges, op.now)
				_, modelErr := model.call(op.method, op.backEdges, op.now)
				if !sameError(actualErr, modelErr) {
					t.Fatalf("rejection mismatch trial=%d step=%d actual=%v model=%v", trial, step, actualErr, modelErr)
				}
			}

			if !oracleEqual(manager, model) {
				t.Fatalf("global mismatch trial=%d step=%d op=%+v cfg=%+v\nactual:\n%s\nmodel:\n%s", trial, step, op, cfg, managerDebug(manager), model.debug())
			}
			if logStep {
				t.Logf("output decision=%s queue=%v LF=%d discarded=%d", model.decision, model.queue, model.last, model.discard)
			}
		}
	}
}

func sameError(left, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Error() == right.Error()
}

func oracleEqual(manager *Manager, model *oracle) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.queue) != len(model.queue) || manager.lastFinish != model.last ||
		manager.discarded != model.discard || manager.now != model.now {
		return false
	}
	for idx := range model.queue {
		if manager.queue[idx] != model.queue[idx] {
			return false
		}
	}
	for idx := range model.methods {
		actual := manager.methods[idx]
		want := model.methods[idx]
		if actual.tier != want.tier || actual.calls != want.calls || actual.backEdges != want.backEdges ||
			actual.deopts != want.deopts || actual.epoch != want.epoch || actual.coolUntil != want.coolUntil ||
			actual.inFlight != want.inFlight {
			return false
		}
	}
	return len(manager.queue) <= int(manager.cfg.QueueCapacity)
}

func managerDebug(manager *Manager) string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	text := fmt.Sprintf("T=%d LF=%d discarded=%d queue=%#v\n", manager.now, manager.lastFinish, manager.discarded, manager.queue)
	for idx, entry := range manager.methods {
		text += fmt.Sprintf("m%d={t=%d i=%d b=%d dc=%d e=%d cu=%d inflight=%t}\n",
			idx, entry.tier, entry.calls, entry.backEdges, entry.deopts, entry.epoch, entry.coolUntil, entry.inFlight)
	}
	return text
}

func (o *oracle) debug() string {
	text := fmt.Sprintf("T=%d LF=%d discarded=%d queue=%#v\n", o.now, o.last, o.discard, o.queue)
	for idx, entry := range o.methods {
		text += fmt.Sprintf("m%d={t=%d i=%d b=%d dc=%d e=%d cu=%d inflight=%t}\n",
			idx, entry.tier, entry.calls, entry.backEdges, entry.deopts, entry.epoch, entry.coolUntil, entry.inFlight)
	}
	return text
}
