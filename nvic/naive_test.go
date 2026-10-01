package nvic

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type naiveNVIC struct {
	m          int
	s          int
	enabled    []bool
	pending    []bool
	active     []bool
	priorities []int
	base       int
	stack      []int
}

func newNaiveNVIC(m, s int) *naiveNVIC {
	return &naiveNVIC{
		m:          m,
		s:          s,
		enabled:    make([]bool, m),
		pending:    make([]bool, m),
		active:     make([]bool, m),
		priorities: make([]int, m),
	}
}

func TestNaiveSkeleton(t *testing.T) {
	if newNaiveNVIC(1, 0).m != 1 {
		t.Fatal("naive model skeleton failed")
	}
}

type testAction struct {
	name  string
	apply func(*Controller) ([]Event, error)
	naive func(*naiveNVIC) ([]Event, error)
}

func TestRandomOperationsMatchNaiveSimulation(t *testing.T) {
	random := rand.New(rand.NewSource(1098))

	for iteration := 0; iteration < 40; iteration++ {
		m := 1 + random.Intn(8)
		s := random.Intn(4)
		c := newTestController(t, m, s)
		n := newNaiveNVIC(m, s)

		for irq := 0; irq < m; irq++ {
			if random.Intn(2) == 0 {
				c.enabled[irq] = true
				n.enabled[irq] = true
			}
		}

		for step := 0; step < 50; step++ {
			action := randomAction(random, m)
			got, gotErr := action.apply(c)
			want, wantErr := action.naive(n)

			candidateIRQ, candidateExists := debugCandidate(c)
			t.Logf(
				"iter=%d step=%d input=%s output=%v reason=candidate=%d/exists=%t runningGroup=%d baseGroup=%d stack=%v",
				iteration, step, action.name, got, candidateIRQ, candidateExists,
				debugRunningLevel(c), debugBaseGroup(c), c.Stack(),
			)

			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("%s: errors differ: got=%v want=%v", action.name, gotErr, wantErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: events differ:\n got=%v\nwant=%v", action.name, got, want)
			}
			assertStateMatchesNaive(t, c, n, action.name)
		}
	}
}

func TestConcurrentOperationsLeaveConsistentState(t *testing.T) {
	c := newTestController(t, 12, 2)

	const goroutines = 16
	const operations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for worker := 0; worker < goroutines; worker++ {
		go func(worker int) {
			defer wg.Done()

			for i := 0; i < operations; i++ {
				irq := (worker*7 + i*3) % c.interruptCount
				switch (worker + i) % 7 {
				case 0:
					_, _ = c.Enable(irq)
				case 1:
					_, _ = c.Disable(irq)
				case 2:
					_, _ = c.Pend(irq)
				case 3:
					_, _ = c.Clear(irq)
				case 4:
					_, _ = c.SetPriority(irq, (worker*17+i*3)%256)
				case 5:
					_, _ = c.SetBase((worker + i) % 256)
				case 6:
					_, _ = c.Return()
				}
			}
		}(worker)
	}

	wg.Wait()
	assertSchedulingInvariant(t, c)
}

func randomAction(random *rand.Rand, m int) testAction {
	irq := random.Intn(m + 2)
	value := random.Intn(258) - 1

	switch random.Intn(8) {
	case 0:
		return enableAction(irq)
	case 1:
		return disableAction(irq)
	case 2:
		return pendAction(irq)
	case 3:
		return clearAction(irq)
	case 4:
		return setPriorityAction(irq, value)
	case 5:
		return setBaseAction(value)
	default:
		return returnAction()
	}
}

func enableAction(irq int) testAction {
	return testAction{
		name:  fmt.Sprintf("Enable(%d)", irq),
		apply: func(c *Controller) ([]Event, error) { return c.Enable(irq) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.enable(irq) },
	}
}

func disableAction(irq int) testAction {
	return testAction{
		name:  fmt.Sprintf("Disable(%d)", irq),
		apply: func(c *Controller) ([]Event, error) { return c.Disable(irq) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.disable(irq) },
	}
}

func pendAction(irq int) testAction {
	return testAction{
		name:  fmt.Sprintf("Pend(%d)", irq),
		apply: func(c *Controller) ([]Event, error) { return c.Pend(irq) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.pend(irq) },
	}
}

func clearAction(irq int) testAction {
	return testAction{
		name:  fmt.Sprintf("Clear(%d)", irq),
		apply: func(c *Controller) ([]Event, error) { return c.Clear(irq) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.clear(irq) },
	}
}

func setPriorityAction(irq, priority int) testAction {
	return testAction{
		name:  fmt.Sprintf("SetPriority(%d,%d)", irq, priority),
		apply: func(c *Controller) ([]Event, error) { return c.SetPriority(irq, priority) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.setPriority(irq, priority) },
	}
}

func setBaseAction(base int) testAction {
	return testAction{
		name:  fmt.Sprintf("SetBase(%d)", base),
		apply: func(c *Controller) ([]Event, error) { return c.SetBase(base) },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.setBase(base) },
	}
}

func returnAction() testAction {
	return testAction{
		name:  "Return()",
		apply: func(c *Controller) ([]Event, error) { return c.Return() },
		naive: func(n *naiveNVIC) ([]Event, error) { return n.returnIRQ() },
	}
}

func debugCandidate(c *Controller) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.candidate()
}

func debugRunningLevel(c *Controller) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.runningLevel()
}

func debugBaseGroup(c *Controller) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.groupPriority(c.basePriority)
}

func assertStateMatchesNaive(t *testing.T, c *Controller, n *naiveNVIC, action string) {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !reflect.DeepEqual(c.enabled, n.enabled) ||
		!reflect.DeepEqual(c.pending, n.pending) ||
		!reflect.DeepEqual(c.active, n.active) ||
		!reflect.DeepEqual(c.priorities, n.priorities) ||
		c.basePriority != n.base ||
		!reflect.DeepEqual(c.stack, n.stack) {
		t.Fatalf(
			"%s: state differs:\n got={enabled:%v pending:%v active:%v priorities:%v base:%d stack:%v}\nwant={enabled:%v pending:%v active:%v priorities:%v base:%d stack:%v}",
			action, c.enabled, c.pending, c.active, c.priorities, c.basePriority, c.stack,
			n.enabled, n.pending, n.active, n.priorities, n.base, n.stack,
		)
	}
	assertSchedulingInvariantLocked(t, c)
}

func assertSchedulingInvariant(t *testing.T, c *Controller) {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()

	assertSchedulingInvariantLocked(t, c)
}

func assertSchedulingInvariantLocked(t *testing.T, c *Controller) {
	t.Helper()

	seen := make(map[int]bool)
	for _, irq := range c.stack {
		if seen[irq] {
			t.Fatalf("IRQ %d duplicated in stack %v", irq, c.stack)
		}
		seen[irq] = true
		if !c.active[irq] {
			t.Fatalf("stack IRQ %d is inactive: active=%v stack=%v", irq, c.active, c.stack)
		}
	}

	for irq, active := range c.active {
		if active && !seen[irq] {
			t.Fatalf("active IRQ %d absent from stack %v", irq, c.stack)
		}
	}

	candidate, exists := c.candidate()
	if exists && c.groupPriority(c.priorities[candidate]) < c.runningLevel() {
		t.Fatalf("unhandled preemptible candidate %d at running level %d", candidate, c.runningLevel())
	}
}

func (n *naiveNVIC) enable(irq int) ([]Event, error) {
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.enabled[irq] = true
	return n.dispatch(), nil
}

func (n *naiveNVIC) disable(irq int) ([]Event, error) {
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.enabled[irq] = false
	return n.dispatch(), nil
}

func (n *naiveNVIC) pend(irq int) ([]Event, error) {
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.pending[irq] = true
	return n.dispatch(), nil
}

func (n *naiveNVIC) clear(irq int) ([]Event, error) {
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	n.pending[irq] = false
	return n.dispatch(), nil
}

func (n *naiveNVIC) setPriority(irq, priority int) ([]Event, error) {
	if err := n.checkIRQ(irq); err != nil {
		return nil, err
	}
	if priority < 0 || priority > 255 {
		return nil, ErrInvalidPriority
	}
	n.priorities[irq] = priority
	return n.dispatch(), nil
}

func (n *naiveNVIC) setBase(base int) ([]Event, error) {
	if base < 0 || base > 255 {
		return nil, ErrInvalidBasePriority
	}
	n.base = base
	return n.dispatch(), nil
}

func (n *naiveNVIC) returnIRQ() ([]Event, error) {
	if len(n.stack) == 0 {
		return nil, ErrEmptyStack
	}

	irq := n.stack[len(n.stack)-1]
	n.stack = n.stack[:len(n.stack)-1]
	n.active[irq] = false
	events := []Event{{Kind: EventExit, IRQ: irq}}

	candidate, exists := n.candidate()
	if exists && (len(n.stack) == 0 || n.group(n.priorities[candidate]) < n.runningLevel()) {
		events = append(events, n.enter(candidate, EventTailChain))
		return events, nil
	}

	if len(n.stack) == 0 {
		events = append(events, Event{Kind: EventIdle})
	} else {
		events = append(events, Event{Kind: EventResume, IRQ: n.stack[len(n.stack)-1]})
	}
	return events, nil
}

func (n *naiveNVIC) checkIRQ(irq int) error {
	if irq < 0 || irq >= n.m {
		return ErrInvalidIRQ
	}
	return nil
}

func (n *naiveNVIC) group(priority int) int {
	return priority >> n.s
}

func (n *naiveNVIC) sub(priority int) int {
	if n.s == 0 {
		return 0
	}
	return priority & (1<<n.s - 1)
}

func (n *naiveNVIC) masked(irq int) bool {
	return n.base > 0 && n.group(n.priorities[irq]) >= n.group(n.base)
}

func (n *naiveNVIC) runningLevel() int {
	if len(n.stack) == 0 {
		return 256
	}
	return n.group(n.priorities[n.stack[len(n.stack)-1]])
}

func (n *naiveNVIC) candidate() (int, bool) {
	best := -1
	for irq := 0; irq < n.m; irq++ {
		if !n.enabled[irq] || !n.pending[irq] || n.active[irq] || n.masked(irq) {
			continue
		}
		if best == -1 || n.less(irq, best) {
			best = irq
		}
	}
	return best, best != -1
}

func (n *naiveNVIC) less(a, b int) bool {
	groupA := n.group(n.priorities[a])
	groupB := n.group(n.priorities[b])
	if groupA != groupB {
		return groupA < groupB
	}

	subA := n.sub(n.priorities[a])
	subB := n.sub(n.priorities[b])
	if subA != subB {
		return subA < subB
	}
	return a < b
}

func (n *naiveNVIC) dispatch() []Event {
	candidate, exists := n.candidate()
	if !exists || n.group(n.priorities[candidate]) >= n.runningLevel() {
		return nil
	}
	return []Event{n.enter(candidate, EventEnter)}
}

func (n *naiveNVIC) enter(irq int, kind EventKind) Event {
	n.pending[irq] = false
	n.active[irq] = true
	n.stack = append(n.stack, irq)

	return Event{
		Kind:    kind,
		IRQ:     irq,
		Preempt: kind == EventEnter && len(n.stack) > 1,
	}
}
