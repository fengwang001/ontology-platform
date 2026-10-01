package nvic

import (
	"errors"
	"sync"
)

var (
	ErrInvalidInterruptCount  = errors.New("nvic: interrupt count must be greater than zero")
	ErrInvalidSubPriorityBits = errors.New("nvic: sub-priority bits must be between 0 and 7")
	ErrInvalidIRQ             = errors.New("nvic: interrupt number out of range")
	ErrInvalidPriority        = errors.New("nvic: priority must be between 0 and 255")
	ErrInvalidBasePriority    = errors.New("nvic: base priority must be between 0 and 255")
	ErrEmptyStack             = errors.New("nvic: cannot return from an empty stack")
)

type EventKind string

// EventKind identifies the scheduling effect produced by an operation.
const (
	EventEnter     EventKind = "Enter"
	EventExit      EventKind = "Exit"
	EventTailChain EventKind = "TailChain"
	EventResume    EventKind = "Resume"
	EventIdle      EventKind = "Idle"
)

type Event struct {
	Kind    EventKind
	IRQ     int
	Preempt bool
}

type Controller struct {
	mu              sync.Mutex
	interruptCount  int
	subPriorityBits int
	enabled         []bool
	pending         []bool
	active          []bool
	priorities      []int
	basePriority    int
	stack           []int
}

// New creates a controller for interruptCount interrupts using subPriorityBits
// low priority bits as sub-priority.
func New(interruptCount, subPriorityBits int) (*Controller, error) {
	if interruptCount < 1 {
		return nil, ErrInvalidInterruptCount
	}
	if subPriorityBits < 0 || subPriorityBits > 7 {
		return nil, ErrInvalidSubPriorityBits
	}

	return &Controller{
		interruptCount:  interruptCount,
		subPriorityBits: subPriorityBits,
		enabled:         make([]bool, interruptCount),
		pending:         make([]bool, interruptCount),
		active:          make([]bool, interruptCount),
		priorities:      make([]int, interruptCount),
	}, nil
}

// Enable marks an interrupt enabled and then evaluates scheduling.
func (c *Controller) Enable(irq int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return nil, err
	}

	c.enabled[irq] = true
	return c.dispatch(), nil
}

// Disable marks an interrupt disabled without affecting an active instance.
func (c *Controller) Disable(irq int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return nil, err
	}

	c.enabled[irq] = false
	return c.dispatch(), nil
}

// Pend marks an interrupt as pending, including when it is already active.
func (c *Controller) Pend(irq int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return nil, err
	}

	c.pending[irq] = true
	return c.dispatch(), nil
}

// Clear removes the pending flag without affecting an active instance.
func (c *Controller) Clear(irq int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return nil, err
	}

	c.pending[irq] = false
	return c.dispatch(), nil
}

// SetPriority updates an interrupt's current 8-bit priority and evaluates scheduling.
func (c *Controller) SetPriority(irq, priority int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return nil, err
	}
	if priority < 0 || priority > 255 {
		return nil, ErrInvalidPriority
	}

	c.priorities[irq] = priority
	return c.dispatch(), nil
}

// SetBase updates the current 8-bit masking threshold.
func (c *Controller) SetBase(basePriority int) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if basePriority < 0 || basePriority > 255 {
		return nil, ErrInvalidBasePriority
	}

	c.basePriority = basePriority
	return c.dispatch(), nil
}

// Return exits the top interrupt and performs tail-chain or resume selection.
func (c *Controller) Return() ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.stack) == 0 {
		return nil, ErrEmptyStack
	}

	irq := c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
	c.active[irq] = false
	events := []Event{{Kind: EventExit, IRQ: irq}}

	if candidate, ok := c.candidate(); ok {
		if len(c.stack) == 0 || c.groupPriority(c.priorities[candidate]) < c.runningLevel() {
			events = append(events, c.enter(candidate, EventTailChain))
			return events, nil
		}
	}

	if len(c.stack) > 0 {
		events = append(events, Event{Kind: EventResume, IRQ: c.stack[len(c.stack)-1]})
	} else {
		events = append(events, Event{Kind: EventIdle})
	}

	return events, nil
}

// Enabled reports whether the interrupt is enabled.
func (c *Controller) Enabled(irq int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return false, err
	}
	return c.enabled[irq], nil
}

// Pending reports whether the interrupt is pending.
func (c *Controller) Pending(irq int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return false, err
	}
	return c.pending[irq], nil
}

// Active reports whether the interrupt is active.
func (c *Controller) Active(irq int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return false, err
	}
	return c.active[irq], nil
}

// Priority returns the interrupt's current 8-bit priority.
func (c *Controller) Priority(irq int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkIRQ(irq); err != nil {
		return 0, err
	}
	return c.priorities[irq], nil
}

// BasePriority returns the current masking threshold.
func (c *Controller) BasePriority() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.basePriority
}

// Stack returns a copy of active interrupt numbers from oldest to newest.
func (c *Controller) Stack() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]int(nil), c.stack...)
}

// GroupPriority returns the group component of a valid 8-bit priority.
func (c *Controller) GroupPriority(priority int) (int, error) {
	if priority < 0 || priority > 255 {
		return 0, ErrInvalidPriority
	}
	return c.groupPriority(priority), nil
}

func (c *Controller) checkIRQ(irq int) error {
	if irq < 0 || irq >= c.interruptCount {
		return ErrInvalidIRQ
	}
	return nil
}

func (c *Controller) groupPriority(priority int) int {
	return priority >> c.subPriorityBits
}

func (c *Controller) subPriority(priority int) int {
	if c.subPriorityBits == 0 {
		return 0
	}
	return priority & (1<<c.subPriorityBits - 1)
}

func (c *Controller) masked(irq int) bool {
	return c.basePriority > 0 &&
		c.groupPriority(c.priorities[irq]) >= c.groupPriority(c.basePriority)
}

func (c *Controller) runningLevel() int {
	if len(c.stack) == 0 {
		return 1 << 8
	}
	return c.groupPriority(c.priorities[c.stack[len(c.stack)-1]])
}

func (c *Controller) candidate() (int, bool) {
	best := -1
	for irq := 0; irq < c.interruptCount; irq++ {
		if !c.enabled[irq] || !c.pending[irq] || c.active[irq] || c.masked(irq) {
			continue
		}
		if best == -1 || c.less(irq, best) {
			best = irq
		}
	}
	return best, best != -1
}

func (c *Controller) less(a, b int) bool {
	groupA := c.groupPriority(c.priorities[a])
	groupB := c.groupPriority(c.priorities[b])
	if groupA != groupB {
		return groupA < groupB
	}
	subA := c.subPriority(c.priorities[a])
	subB := c.subPriority(c.priorities[b])
	if subA != subB {
		return subA < subB
	}
	return a < b
}

func (c *Controller) dispatch() []Event {
	candidate, ok := c.candidate()
	if !ok || c.groupPriority(c.priorities[candidate]) >= c.runningLevel() {
		return nil
	}

	return []Event{c.enter(candidate, EventEnter)}
}

func (c *Controller) enter(irq int, kind EventKind) Event {
	c.pending[irq] = false
	c.active[irq] = true
	preempt := kind == EventEnter && len(c.stack) > 0
	c.stack = append(c.stack, irq)

	return Event{Kind: kind, IRQ: irq, Preempt: preempt}
}
