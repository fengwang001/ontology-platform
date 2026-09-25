// Package api is the public surface: New, Submit, Run, Wait, SelfCheck; it
// depends only on sched and keeps all state in process memory.
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/sched"
)

// Distinct sentinel errors; callers decide with errors.Is.
var (
	ErrEmptyID           = errors.New("api: empty job id")
	ErrDuplicateID       = sched.ErrDuplicateID
	ErrNonPositiveLength = errors.New("api: length must be > 0")
	ErrNegativeArrive    = errors.New("api: arrive must be >= 0")
)

// Interval is one executed CPU interval [Start, Finish).
type Interval struct {
	ID            string
	Start, Finish int64
}

// API is a non-preemptive SJF scheduler.
type API struct{ sc *sched.Scheduler }

func New() *API { return &API{sc: sched.New()} }

// Submit registers (id, arrive, length). A rejected call fails as a whole
// before any state changes; the scheduler stays usable afterwards.
func (a *API) Submit(id string, arrive, length int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if length <= 0 {
		return ErrNonPositiveLength
	}
	if arrive < 0 {
		return ErrNegativeArrive
	}
	return a.sc.Add(id, arrive, length) // duplicate id checked before insert
}

func (a *API) Run() []string        { return a.sc.Run() }
func (a *API) Wait(id string) int64 { return a.sc.Wait(id) }

// Intervals returns executed intervals in dispatch order.
func (a *API) Intervals() []Interval {
	ts := a.sc.Trace()
	out := make([]Interval, len(ts))
	for i, t := range ts {
		out[i] = Interval{t.ID, t.Start, t.Finish}
	}
	return out
}

// Built-in six-job sequence from the task.
var (
	bIDs    = []string{"X", "L", "A", "B", "C", "D"}
	bArrive = []int64{0, 1, 2, 3, 4, 5}
	bLength = []int64{5, 10, 2, 3, 2, 1}

	builtinOrder = []string{"X", "D", "A", "C", "B", "L"}

	builtinIntervals = []Interval{
		{"X", 0, 5}, {"D", 5, 6}, {"A", 6, 8},
		{"C", 8, 10}, {"B", 10, 13}, {"L", 13, 23},
	}
)

// naiveRef is the tick-by-tick reference: when the CPU is idle, scan every
// admitted unfinished job and pick the (length, registration) minimum.
func naiveRef(ids []string, arrive, length []int64) []string {
	done := make([]bool, len(ids))
	out := []string{}
	for t, n := int64(0), 0; n < len(ids); {
		cur := -1
		for i := range ids {
			if !done[i] && arrive[i] <= t && (cur < 0 || length[i] < length[cur] ||
				length[i] == length[cur] && i < cur) {
				cur = i
			}
		}
		if cur < 0 {
			nt := int64(-1)
			for i := range ids {
				if !done[i] && (nt < 0 || arrive[i] < nt) {
					nt = arrive[i]
				}
			}
			t = nt
			continue
		}
		t, done[cur], n = t+length[cur], true, n+1
		out = append(out, ids[cur])
	}
	return out
}

// SelfCheck runs the built-in six-job sequence on a fresh scheduler and
// verifies the four invariants: naive equivalence, non-preemption, minimum
// selection with registration-order tie break, and rejection without trace.
func (a *API) SelfCheck() error {
	c := New()
	for i := range bIDs {
		if err := c.Submit(bIDs[i], bArrive[i], bLength[i]); err != nil {
			return err
		}
	}
	order := c.Run()
	if !slices.Equal(order, builtinOrder) {
		return fmt.Errorf("selfcheck: order %v, want %v", order, builtinOrder)
	}
	if !slices.Equal(order, naiveRef(bIDs, bArrive, bLength)) {
		return errors.New("selfcheck: order disagrees with naive reference")
	}
	iv := c.Intervals()
	if len(iv) != len(builtinIntervals) {
		return fmt.Errorf("selfcheck: %d intervals, want %d", len(iv), len(builtinIntervals))
	}
	for i, w := range builtinIntervals {
		if iv[i] != w {
			return fmt.Errorf("selfcheck: interval %d = %+v, want %+v", i, iv[i], w)
		}
	}
	if order[2] != "A" || order[3] != "C" {
		return errors.New("selfcheck: tie not broken by registration order")
	}
	if w := c.Wait("L"); w != 12 {
		return fmt.Errorf("selfcheck: L wait = %d, want 12", w)
	}
	for i, e := range []error{
		c.Submit("", 0, 1), c.Submit("X", 0, 1),
		c.Submit("Q", 0, 0), c.Submit("Q", -1, 1),
	} {
		if e == nil {
			return fmt.Errorf("selfcheck: bad submit %d accepted", i)
		}
	}
	if got := c.Run(); !slices.Equal(got, builtinOrder) {
		return fmt.Errorf("selfcheck: state changed after rejection: %v", got)
	}
	return nil
}
