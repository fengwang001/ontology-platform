package ontology

import (
	"math"
	"math/rand/v2"
	"sync"
	"testing"
)

func testConfig() StallConfig {
	return StallConfig{
		Level0Slow:     8,
		Level0Stop:     16,
		PendingSlow:    80,
		PendingStop:    160,
		FrozenStop:     8,
		MaxDelayMicros: 10,
	}
}

func newTestController(t *testing.T) *WriteStallController {
	t.Helper()
	controller, reason := NewWriteStallController(testConfig())
	if reason != StallRejectNone {
		t.Fatalf("NewWriteStallController returned rejection: %v", reason)
	}
	return controller
}

func observeResult(t *testing.T, controller *WriteStallController, n0 int64, pending int64, frozen int64, wantState StallState, wantDelay int64) StallResult {
	t.Helper()
	result := controller.Observe(n0, pending, frozen)
	if result.Rejected {
		t.Fatalf("Observe(%d, %d, %d) rejected: %v", n0, pending, frozen, result.Reason)
	}
	if result.State != wantState || result.DelayMicros != wantDelay {
		t.Fatalf("Observe(%d, %d, %d) = state %s delay %d, want state %s delay %d",
			n0, pending, frozen, result.State, result.DelayMicros, wantState, wantDelay)
	}
	return result
}

func stopWithLevel0(t *testing.T, controller *WriteStallController) {
	observeResult(t, controller, 16, 79, 7, StallStopped, 0)
}

func stopWithPending(t *testing.T, controller *WriteStallController) {
	observeResult(t, controller, 7, 160, 7, StallStopped, 0)
}

func stopWithFrozen(t *testing.T, controller *WriteStallController) {
	observeResult(t, controller, 7, 79, 8, StallStopped, 0)
}

func slowController(t *testing.T, controller *WriteStallController) {
	observeResult(t, controller, 8, 80, 7, StallSlow, 1)
}

func TestThresholdEqualityAndOffByOne(t *testing.T) {
	tests := []struct {
		name       string
		n0         int64
		pending    int64
		frozen     int64
		wantState  StallState
		wantDelay  int64
		wantReason StallRejectReason
	}{
		{"normal one below slow", 7, 79, 7, StallNormal, 0, StallRejectNone},
		{"level0 slow exact", 8, 79, 7, StallSlow, 1, StallRejectNone},
		{"pending slow exact", 7, 80, 7, StallSlow, 1, StallRejectNone},
		{"level0 stop one below", 15, 79, 7, StallSlow, 9, StallRejectNone},
		{"pending stop one below", 7, 159, 7, StallSlow, 10, StallRejectNone},
		{"level0 stop exact", 16, 79, 7, StallStopped, 0, StallRejectWriteStopped},
		{"pending stop exact", 7, 160, 7, StallStopped, 0, StallRejectWriteStopped},
		{"frozen stop exact", 7, 79, 8, StallStopped, 0, StallRejectWriteStopped},
		{"level0 stop one over", 17, 79, 7, StallStopped, 0, StallRejectWriteStopped},
		{"pending stop one over", 7, 161, 7, StallStopped, 0, StallRejectWriteStopped},
		{"frozen stop one over", 7, 79, 9, StallStopped, 0, StallRejectWriteStopped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := newTestController(t)
			result := controller.Observe(tt.n0, tt.pending, tt.frozen)
			if result.State != tt.wantState || result.DelayMicros != tt.wantDelay || result.Rejected {
				t.Fatalf("Observe = %+v, want state %s delay %d", result, tt.wantState, tt.wantDelay)
			}
			admit := controller.Admit()
			wantRejected := tt.wantReason == StallRejectWriteStopped
			if admit.Rejected != wantRejected || admit.Reason != tt.wantReason {
				t.Fatalf("Admit = %+v, want rejected=%t reason=%v", admit, wantRejected, tt.wantReason)
			}
		})
	}
}

func TestRecoveryLinesEqualityAndOffByOne(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T, *WriteStallController)
		n0        int64
		pending   int64
		frozen    int64
		wantState StallState
		wantDelay int64
	}{
		{"slow stays at S1 recovery", slowController, 6, 79, 7, StallSlow, 1},
		{"slow exits one below S1 recovery", slowController, 5, 59, 7, StallNormal, 0},
		{"slow stays at P1 recovery", slowController, 7, 60, 7, StallSlow, 1},
		{"slow exits one below P1 recovery", slowController, 5, 59, 7, StallNormal, 0},
		{"stopped stays at S2 recovery", stopWithLevel0, 12, 79, 7, StallStopped, 0},
		{"stopped exits one below S2 recovery to normal", stopWithLevel0, 7, 79, 5, StallNormal, 0},
		{"stopped stays at P2 recovery", stopWithPending, 7, 120, 7, StallStopped, 0},
		{"stopped exits one below P2 recovery to normal", stopWithPending, 7, 79, 5, StallNormal, 0},
		{"stopped stays at frozen recovery", stopWithFrozen, 11, 119, 6, StallStopped, 0},
		{"stopped exits one below frozen recovery to normal", stopWithFrozen, 7, 79, 5, StallNormal, 0},
		{"stopped exits one below S2 recovery to slow", stopWithLevel0, 11, 79, 5, StallSlow, 4},
		{"stopped exits one below P2 recovery to slow", stopWithPending, 7, 119, 5, StallSlow, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := newTestController(t)
			tt.setup(t, controller)
			observeResult(t, controller, tt.n0, tt.pending, tt.frozen, tt.wantState, tt.wantDelay)
		})
	}
}

func TestAllTransitions(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T, *WriteStallController)
		n0        int64
		pending   int64
		frozen    int64
		wantState StallState
		wantDelay int64
	}{
		{"normal to normal", func(t *testing.T, c *WriteStallController) {}, 7, 79, 7, StallNormal, 0},
		{"normal to slow", func(t *testing.T, c *WriteStallController) {}, 8, 79, 7, StallSlow, 1},
		{"normal to stopped", func(t *testing.T, c *WriteStallController) {}, 16, 79, 7, StallStopped, 0},
		{"slow to normal", slowController, 5, 59, 7, StallNormal, 0},
		{"slow to slow", slowController, 6, 60, 7, StallSlow, 1},
		{"slow to stopped", slowController, 16, 79, 7, StallStopped, 0},
		{"stopped to normal", stopWithLevel0, 7, 79, 5, StallNormal, 0},
		{"stopped to slow", stopWithLevel0, 11, 79, 5, StallSlow, 4},
		{"stopped to stopped", stopWithLevel0, 12, 119, 5, StallStopped, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := newTestController(t)
			tt.setup(t, controller)
			observeResult(t, controller, tt.n0, tt.pending, tt.frozen, tt.wantState, tt.wantDelay)
		})
	}
}

func TestSlowDelayUsesMaximumRatioAndCeiling(t *testing.T) {
	controller := newTestController(t)

	observeResult(t, controller, 8, 80, 7, StallSlow, 1)
	observeResult(t, controller, 9, 80, 7, StallSlow, 2)
	observeResult(t, controller, 11, 80, 7, StallSlow, 4)
	observeResult(t, controller, 11, 120, 7, StallSlow, 5)
	observeResult(t, controller, 12, 120, 7, StallSlow, 5)
	observeResult(t, controller, 15, 80, 7, StallSlow, 9)
	observeResult(t, controller, 7, 159, 7, StallSlow, 10)
	observeResult(t, controller, 6, 60, 7, StallSlow, 1)

	if got := slowDelay(1, math.MaxInt64, 0, 0, 1, 0, 1); got != 1 {
		t.Fatalf("upper-clamped delay = %d, want 1", got)
	}
	if got := slowDelay(1, -math.MaxInt64, 0, 0, 1, 0, 1); got != 1 {
		t.Fatalf("lower-clamped delay = %d, want 1", got)
	}
}

func TestValidationOrderAndRejectedObserveKeepsState(t *testing.T) {
	configs := []struct {
		name   string
		config StallConfig
		want   StallRejectReason
	}{
		{"S1 not less than S2", StallConfig{16, 16, 80, 160, 8, 10}, StallRejectS1},
		{"P1 not less than P2", StallConfig{8, 16, 160, 160, 8, 10}, StallRejectP1},
		{"I non-positive", StallConfig{8, 16, 80, 160, 0, 10}, StallRejectImm},
		{"D non-positive", StallConfig{8, 16, 80, 160, 8, 0}, StallRejectD},
	}

	for _, tt := range configs {
		t.Run(tt.name, func(t *testing.T) {
			controller, reason := NewWriteStallController(tt.config)
			if controller != nil || reason != tt.want {
				t.Fatalf("NewWriteStallController = (%v, %v), want (nil, %v)", controller, reason, tt.want)
			}
		})
	}

	controller := newTestController(t)
	observeResult(t, controller, 10, 100, 7, StallSlow, 3)

	negativeTests := []struct {
		n0      int64
		pending int64
		frozen  int64
		want    StallRejectReason
	}{
		{-1, -1, -1, StallRejectN0},
		{10, -1, -1, StallRejectPending},
		{10, 100, -1, StallRejectFrozen},
	}
	for _, tt := range negativeTests {
		result := controller.Observe(tt.n0, tt.pending, tt.frozen)
		if !result.Rejected || result.Reason != tt.want {
			t.Fatalf("Observe(%d, %d, %d) = %+v, want rejection %v", tt.n0, tt.pending, tt.frozen, result, tt.want)
		}
	}

	observeResult(t, controller, 10, 100, 7, StallSlow, 3)
	observeResult(t, controller, 16, 79, 7, StallStopped, 0)
	admit := controller.Admit()
	if !admit.Rejected || admit.Reason != StallRejectWriteStopped {
		t.Fatalf("stopped Admit = %+v, want write-stopped rejection", admit)
	}
}

func TestConcurrentObserveAdmitAndQueries(t *testing.T) {
	controller := newTestController(t)
	observations := [][3]int64{
		{7, 79, 7},
		{10, 100, 7},
		{16, 79, 7},
		{11, 119, 5},
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(3)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				observation := observations[(worker+i)%len(observations)]
				result := controller.Observe(observation[0], observation[1], observation[2])
				if result.State < StallNormal || result.State > StallStopped {
					t.Errorf("invalid state: %v", result.State)
				}
				if result.DelayMicros < 0 {
					t.Errorf("negative delay: %d", result.DelayMicros)
				}
			}
		}(worker)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				result := controller.Admit()
				if result.Rejected && result.State != StallStopped {
					t.Errorf("non-stopped admission rejected: %+v", result)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				state := controller.State()
				delay := controller.DelayMicros()
				if state < StallNormal || state > StallStopped || delay < 0 {
					t.Errorf("invalid query: state=%v delay=%d", state, delay)
				}
			}
		}()
	}
	wg.Wait()
}

type naiveWriteStall struct {
	config StallConfig
	state  StallState
	delay  int64
}

type naiveResult struct {
	state  StallState
	delay  int64
	reason StallRejectReason
	basis  string
}

func (m *naiveWriteStall) observe(n0 int64, pending int64, frozen int64) naiveResult {
	if n0 < 0 {
		return naiveResult{state: m.state, delay: m.delay, reason: StallRejectN0, basis: "reject before state read: n0 is negative"}
	}
	if pending < 0 {
		return naiveResult{state: m.state, delay: m.delay, reason: StallRejectPending, basis: "reject before state read: pending is negative"}
	}
	if frozen < 0 {
		return naiveResult{state: m.state, delay: m.delay, reason: StallRejectFrozen, basis: "reject before state read: frozen is negative"}
	}

	stopByLevel0 := n0 >= m.config.Level0Stop
	stopByPending := pending >= m.config.PendingStop
	stopByFrozen := frozen >= m.config.FrozenStop
	if stopByLevel0 || stopByPending || stopByFrozen {
		m.state = StallStopped
		m.delay = 0
		return naiveResult{state: m.state, delay: m.delay, basis: "rule 1: a stop condition is true"}
	}

	slow := n0 >= m.config.Level0Slow || pending >= m.config.PendingSlow
	if m.state == StallStopped {
		level0Recovered := n0 < 3*m.config.Level0Stop/4
		pendingRecovered := pending < 3*m.config.PendingStop/4
		frozenRecovered := frozen < 3*m.config.FrozenStop/4
		if !(level0Recovered && pendingRecovered && frozenRecovered) {
			return naiveResult{state: m.state, delay: m.delay, basis: "rule 2: stopped and at least one stop recovery line is not strictly below"}
		}
		if slow {
			m.state = StallSlow
			m.delay = naiveSlowDelay(m.config.MaxDelayMicros, n0, pending, m.config.Level0Slow, m.config.Level0Stop, m.config.PendingSlow, m.config.PendingStop)
			return naiveResult{state: m.state, delay: m.delay, basis: "rule 2: left stopped and slow condition remains true"}
		}
		m.state = StallNormal
		m.delay = 0
		return naiveResult{state: m.state, delay: m.delay, basis: "rule 2: left stopped directly to normal"}
	}

	if slow {
		m.state = StallSlow
		m.delay = naiveSlowDelay(m.config.MaxDelayMicros, n0, pending, m.config.Level0Slow, m.config.Level0Stop, m.config.PendingSlow, m.config.PendingStop)
		return naiveResult{state: m.state, delay: m.delay, basis: "rule 3: slow condition is true"}
	}

	if m.state == StallSlow {
		level0Recovered := n0 < 3*m.config.Level0Slow/4
		pendingRecovered := pending < 3*m.config.PendingSlow/4
		if level0Recovered && pendingRecovered {
			m.state = StallNormal
			m.delay = 0
			return naiveResult{state: m.state, delay: m.delay, basis: "rule 4: below both slow recovery lines"}
		}
		m.delay = naiveSlowDelay(m.config.MaxDelayMicros, n0, pending, m.config.Level0Slow, m.config.Level0Stop, m.config.PendingSlow, m.config.PendingStop)
		return naiveResult{state: m.state, delay: m.delay, basis: "rule 4: slow hysteresis keeps slow state"}
	}

	m.state = StallNormal
	m.delay = 0
	return naiveResult{state: m.state, delay: m.delay, basis: "rule 5: normal condition"}
}

func naiveSlowDelay(maxDelay int64, n0 int64, pending int64, s1 int64, s2 int64, p1 int64, p2 int64) int64 {
	level0Numerator := n0 - s1
	level0Denominator := s2 - s1
	pendingNumerator := pending - p1
	pendingDenominator := p2 - p1
	numerator := level0Numerator
	denominator := level0Denominator
	if level0Numerator*pendingDenominator < pendingNumerator*level0Denominator {
		numerator = pendingNumerator
		denominator = pendingDenominator
	}
	if numerator <= 0 {
		return 1
	}
	if numerator >= denominator {
		return maxDelay
	}
	delay := (maxDelay*numerator + denominator - 1) / denominator
	if delay < 1 {
		return 1
	}
	return delay
}

func TestRandom3000StepsMatchesNaiveStateMachineAndReplays(t *testing.T) {
	const steps = 3000
	config := testConfig()
	actual, reason := NewWriteStallController(config)
	if reason != StallRejectNone {
		t.Fatalf("constructor rejected: %v", reason)
	}
	reference := &naiveWriteStall{config: config, state: StallNormal}
	rng := rand.New(rand.NewPCG(0x4c534d5f31303137, 0x5772697465537461))
	inputs := make([][3]int64, steps)
	firstRun := make([]StallResult, steps)

	t.Logf("random differential test: steps=%d config=%+v", steps, config)
	for i := range inputs {
		n0 := int64(rng.IntN(24))
		pending := int64(rng.IntN(240))
		frozen := int64(rng.IntN(13))
		switch rng.IntN(12) {
		case 0:
			n0 = -1
		case 1:
			pending = -1
		case 2:
			frozen = -1
		case 3:
			n0, pending, frozen = -1, -1, -1
		}
		inputs[i] = [3]int64{n0, pending, frozen}

		result := actual.Observe(n0, pending, frozen)
		expected := reference.observe(n0, pending, frozen)
		admit := actual.Admit()
		firstRun[i] = result

		t.Logf("step=%04d input=(n0=%d,pending=%d,frozen=%d) output=(state=%s,delay=%d,rejected=%t,reason=%q) admit=(rejected=%t,reason=%q) basis=%q",
			i, n0, pending, frozen, result.State, result.DelayMicros, result.Rejected, result.Reason,
			admit.Rejected, admit.Reason, expected.basis)

		if result.State != expected.state || result.DelayMicros != expected.delay || result.Reason != expected.reason {
			t.Fatalf("step %d actual=(%s,%d,%v) reference=(%s,%d,%v); basis: %s",
				i, result.State, result.DelayMicros, result.Reason,
				expected.state, expected.delay, expected.reason, expected.basis)
		}
	}

	replay, reason := NewWriteStallController(config)
	if reason != StallRejectNone {
		t.Fatalf("replay constructor rejected: %v", reason)
	}
	for i, input := range inputs {
		result := replay.Observe(input[0], input[1], input[2])
		if result != firstRun[i] {
			t.Fatalf("step %d replay = %+v, first run = %+v", i, result, firstRun[i])
		}
	}
}
