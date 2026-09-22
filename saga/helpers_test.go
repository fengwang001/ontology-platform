package saga

import (
	"context"
	"sync"
	"sync/atomic"

	"ontology/journal"
	"ontology/step"
)

type counters struct {
	fwd   []*int64
	comp  []*int64
}

func newCounters(n int) *counters {
	c := &counters{fwd: make([]*int64, n), comp: make([]*int64, n)}
	for i := 0; i < n; i++ {
		var f, p int64
		c.fwd[i], c.comp[i] = &f, &p
	}
	return c
}

func (c *counters) forward(i int, err error) step.Action {
	return func(ctx context.Context, _ string, _ int, _ int64) error {
		atomic.AddInt64(c.fwd[i], 1)
		return err
	}
}

func (c *counters) compensate(i int, err error) step.Action {
	return func(ctx context.Context, _ string, _ int, _ int64) error {
		atomic.AddInt64(c.comp[i], 1)
		return err
	}
}

func (c *counters) fwdTotal() int {
	var n int64
	for _, p := range c.fwd {
		n += atomic.LoadInt64(p)
	}
	return int(n)
}

func (c *counters) compTotal() int {
	var n int64
	for _, p := range c.comp {
		n += atomic.LoadInt64(p)
	}
	return int(n)
}

func (c *counters) fwdAt(i int) int { return int(atomic.LoadInt64(c.fwd[i])) }
func (c *counters) compAt(i int) int { return int(atomic.LoadInt64(c.comp[i])) }

func buildSteps(c *counters, fwdErr, compErr map[int]error, retries map[int]int, nilComp map[int]bool) []step.Step {
	n := len(c.fwd)
	ss := make([]step.Step, n)
	for i := 0; i < n; i++ {
		st := step.Step{
			Name:    "step", IdemKey: "k" + itoa(i),
			Do: c.forward(i, fwdErr[i]),
		}
		if retries != nil {
			st.Retries = retries[i]
		}
		if !nilComp[i] {
			st.Compensate = c.compensate(i, compErr[i])
		}
		ss[i] = st
	}
	return ss
}

func newOrchestrator(maxJournal int) (*Orchestrator, *journal.Journal) {
	j := journal.New(maxJournal)
	clock := int64(1)
	var mu sync.Mutex
	o, err := New(Config{
		MaxSteps:       100000,
		MaxStepRetries: 10,
		MaxJournalRows: maxJournal,
		Now: func() int64 {
			mu.Lock()
			defer mu.Unlock()
			clock++
			return clock
		},
	}, j)
	if err != nil {
		panic(err)
	}
	return o, j
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
