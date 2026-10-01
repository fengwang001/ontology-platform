package nvic

import (
	"errors"
	"reflect"
	"testing"
)

func TestSameGroupSubpriorityDoesNotPreemptButWinsCandidacy(t *testing.T) {
	c := newTestController(t, 3, 1)

	ok(t, func() ([]Event, error) { return c.SetPriority(1, 0x01) })
	ok(t, func() ([]Event, error) { return c.Enable(1) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(1) }), Event{Kind: EventEnter, IRQ: 1})
	ok(t, func() ([]Event, error) { return c.Enable(0) })
	ok(t, func() ([]Event, error) { return c.Pend(0) })

	if stack := c.Stack(); !reflect.DeepEqual(stack, []int{1}) {
		t.Fatalf("same-group sub-priority must not preempt: stack=%v", stack)
	}

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 1},
		Event{Kind: EventTailChain, IRQ: 0},
	)
}

func TestBasePriorityMasksByGroup(t *testing.T) {
	c := newTestController(t, 17, 0)

	ok(t, func() ([]Event, error) { return c.SetPriority(16, 16) })
	ok(t, func() ([]Event, error) { return c.SetBase(16) })
	ok(t, func() ([]Event, error) { return c.Enable(16) })
	ok(t, func() ([]Event, error) { return c.Pend(16) })

	if pending, _ := c.Pending(16); !pending {
		t.Fatal("interrupt at exactly g(base) must be masked")
	}

	assertEvents(t, ok(t, func() ([]Event, error) { return c.SetBase(0) }), Event{Kind: EventEnter, IRQ: 16})
}

func TestTailChainFromThreadMode(t *testing.T) {
	c := newTestController(t, 2, 0)

	ok(t, func() ([]Event, error) { return c.Enable(0) })
	ok(t, func() ([]Event, error) { return c.Enable(1) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	ok(t, func() ([]Event, error) { return c.Pend(1) })

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 0},
		Event{Kind: EventTailChain, IRQ: 1},
	)
}

func TestReturnSelectsResumeOrTailChain(t *testing.T) {
	t.Run("resume preempted handler", func(t *testing.T) {
		c := newNestedController(t)

		assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
			Event{Kind: EventExit, IRQ: 1},
			Event{Kind: EventResume, IRQ: 0},
		)
	})

	t.Run("tail chain lower candidate", func(t *testing.T) {
		c := newNestedController(t)
		ok(t, func() ([]Event, error) { return c.SetPriority(2, 3) })
		ok(t, func() ([]Event, error) { return c.Enable(2) })
		ok(t, func() ([]Event, error) { return c.Pend(2) })

		assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
			Event{Kind: EventExit, IRQ: 1},
			Event{Kind: EventTailChain, IRQ: 2},
		)
	})
}

func TestSameGroupWaitsForHandlerExit(t *testing.T) {
	c := newTestController(t, 3, 0)

	ok(t, func() ([]Event, error) { return c.SetPriority(0, 2) })
	ok(t, func() ([]Event, error) { return c.SetPriority(1, 1) })
	ok(t, func() ([]Event, error) { return c.SetPriority(2, 2) })
	for _, irq := range []int{0, 1, 2} {
		ok(t, func() ([]Event, error) { return c.Enable(irq) })
	}

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(1) }), Event{Kind: EventEnter, IRQ: 1, Preempt: true})
	ok(t, func() ([]Event, error) { return c.Pend(2) })

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 1},
		Event{Kind: EventResume, IRQ: 0},
	)
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 0},
		Event{Kind: EventTailChain, IRQ: 2},
	)
}

func TestActiveIRQCanPendItselfAndTailChainToItself(t *testing.T) {
	c := newTestController(t, 1, 0)

	ok(t, func() ([]Event, error) { return c.Enable(0) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	ok(t, func() ([]Event, error) { return c.Pend(0) })

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 0},
		Event{Kind: EventTailChain, IRQ: 0},
	)
}

func TestSetPriorityCanMakePendingIRQPreemptImmediately(t *testing.T) {
	c := newTestController(t, 2, 0)

	ok(t, func() ([]Event, error) { return c.SetPriority(0, 2) })
	ok(t, func() ([]Event, error) { return c.SetPriority(1, 5) })
	ok(t, func() ([]Event, error) { return c.Enable(0) })
	ok(t, func() ([]Event, error) { return c.Enable(1) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	ok(t, func() ([]Event, error) { return c.Pend(1) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.SetPriority(1, 1) }),
		Event{Kind: EventEnter, IRQ: 1, Preempt: true},
	)
}

func TestDisableAndClearDoNotAffectActiveIRQ(t *testing.T) {
	c := newTestController(t, 1, 0)

	ok(t, func() ([]Event, error) { return c.Enable(0) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	ok(t, func() ([]Event, error) { return c.Pend(0) })
	ok(t, func() ([]Event, error) { return c.Disable(0) })
	ok(t, func() ([]Event, error) { return c.Clear(0) })

	if active, _ := c.Active(0); !active {
		t.Fatal("Disable must not deactivate a running interrupt")
	}
	if stack := c.Stack(); !reflect.DeepEqual(stack, []int{0}) {
		t.Fatalf("Disable/Clear changed active stack: %v", stack)
	}

	assertEvents(t, ok(t, func() ([]Event, error) { return c.Return() }),
		Event{Kind: EventExit, IRQ: 0},
		Event{Kind: EventIdle},
	)
}

func TestValidationRejectsWithoutMutation(t *testing.T) {
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidInterruptCount) {
		t.Fatalf("M=0: %v", err)
	}
	if _, err := New(1, 8); !errors.Is(err, ErrInvalidSubPriorityBits) {
		t.Fatalf("s=8: %v", err)
	}

	c := newTestController(t, 2, 0)

	if _, err := c.SetPriority(9, -1); !errors.Is(err, ErrInvalidIRQ) {
		t.Fatalf("IRQ validation must take priority order: %v", err)
	}
	if _, err := c.SetPriority(0, 256); !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("priority=256: %v", err)
	}
	if _, err := c.SetBase(256); !errors.Is(err, ErrInvalidBasePriority) {
		t.Fatalf("base=256: %v", err)
	}
	if _, err := c.Pend(2); !errors.Is(err, ErrInvalidIRQ) {
		t.Fatalf("IRQ=2: %v", err)
	}
	if _, err := c.Return(); !errors.Is(err, ErrEmptyStack) {
		t.Fatalf("empty return: %v", err)
	}

	if stack := c.Stack(); len(stack) != 0 || c.BasePriority() != 0 {
		t.Fatalf("rejected operations mutated controller: stack=%v base=%d", stack, c.BasePriority())
	}
}

func newTestController(t *testing.T, interruptCount, subPriorityBits int) *Controller {
	t.Helper()

	c, err := New(interruptCount, subPriorityBits)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newNestedController(t *testing.T) *Controller {
	t.Helper()

	c := newTestController(t, 3, 0)
	ok(t, func() ([]Event, error) { return c.SetPriority(0, 4) })
	ok(t, func() ([]Event, error) { return c.SetPriority(1, 2) })
	ok(t, func() ([]Event, error) { return c.Enable(0) })
	ok(t, func() ([]Event, error) { return c.Enable(1) })
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(0) }), Event{Kind: EventEnter, IRQ: 0})
	assertEvents(t, ok(t, func() ([]Event, error) { return c.Pend(1) }), Event{Kind: EventEnter, IRQ: 1, Preempt: true})
	return c
}

func ok(t *testing.T, operation func() ([]Event, error)) []Event {
	t.Helper()
	events, err := operation()
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func assertEvents(t *testing.T, got []Event, want ...Event) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events differ:\n got=%v\nwant=%v", got, want)
	}
}
