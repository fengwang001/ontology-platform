// 演示程序：go run ./cmd/demo，不读参数、不联网，退出码 0 表示全部 OK。
package main

import (
	"errors"
	"fmt"

	"ontology/journal"
	"ontology/saga"
	"ontology/step"
)

var fail = errors.New("definite failure")

type rec struct {
	fwd map[string]int
	cmp map[string]int
	ord *[]string
}

func (r *rec) s(key string, fwdErr error) step.Step {
	r.fwd[key] = 0
	r.cmp[key] = 0
	return step.Step{
		Key: key,
		Forward: func() error {
			r.fwd[key]++
			return fwdErr
		},
		Compensate: func() error {
			r.cmp[key]++
			*r.ord = append(*r.ord, key)
			return nil
		},
	}
}

var pass = true

func check(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag, pass = "FAIL", false
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func newOrch(cfg saga.Config) (*saga.Orchestrator, func() int64) {
	var clock int64
	now := func() int64 { return clock }
	o, err := saga.New(now, cfg)
	if err != nil {
		panic(err)
	}
	return o, now
}

func main() {
	demoHappyPath()
	demoReverseCompensation()
	demoCompensationFailureContinues()
	demoResumeIdempotent()
	demoRebuildFromJournal()
	demoDeterministicErrors()
	demoConcurrentIsolation()
	demoReadCounts()

	if !pass {
		fmt.Println("DEMO FAILED")
		return
	}
	fmt.Println("ALL DEMOS OK")
}

func demoHappyPath() {
	o, _ := newOrch(saga.Config{})
	c := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
	st, err := o.Run("ok", []step.Step{c.s("a", nil), c.s("b", nil)})
	check("all-success forward path", err == nil && st.Status == saga.Succeeded,
		fmt.Sprintf("status=%s", st.Status))
}

func demoReverseCompensation() {
	o, _ := newOrch(saga.Config{})
	c := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
	steps := []step.Step{
		c.s("a", nil), c.s("b", nil),
		{Key: "c", Forward: func() error { c.fwd["c"]++; return step.NewDefiniteFailure(fail) },
			Compensate: func() error { c.cmp["c"]++; *c.ord = append(*c.ord, "c"); return nil }},
	}
	st, _ := o.Run("rc", steps)
	ok := st.Status == saga.Compensated &&
		fmt.Sprint(*c.ord) == "[b a]" && c.cmp["c"] == 0
	check("reverse compensation (failed step skipped)", ok,
		fmt.Sprintf("order=%v failedStepCompensated=%d", *c.ord, c.cmp["c"]))
}

func demoCompensationFailureContinues() {
	o, _ := newOrch(saga.Config{})
	r := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
	steps := []step.Step{
		r.s("a", nil),
		{Key: "b", Forward: func() error { r.fwd["b"]++; return nil },
			Compensate: func() error { r.cmp["b"]++; *r.ord = append(*r.ord, "b"); return fail }},
		r.s("c", nil),
		{Key: "d", Forward: func() error { return step.NewDefiniteFailure(fail) }},
	}
	st, _ := o.Run("cf", steps)
	ok := st.Status == saga.CompensateFailed &&
		fmt.Sprint(st.CompensateFailures) == "[1]" &&
		fmt.Sprint(*r.ord) == "[c b a]"
	check("compensation failure does not swallow remaining", ok,
		fmt.Sprintf("order=%v failures=%v", *r.ord, st.CompensateFailures))
}

func demoResumeIdempotent() {
	o, _ := newOrch(saga.Config{})
	c := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
	steps := []step.Step{c.s("a", nil), c.s("b", nil),
		{Key: "x", Forward: func() error { return step.NewDefiniteFailure(fail) }}}
	_, _ = o.Run("ri", steps)
	before, _ := o.Calls("ri")
	for i := 0; i < 5; i++ {
		if _, err := o.Resume("ri"); err != nil {
			check("repeated Resume keeps call counts stable", false, err.Error())
			return
		}
	}
	after, _ := o.Calls("ri")
	check("repeated Resume keeps call counts stable",
		before.ForwardAll == after.ForwardAll && before.CompensateAll == after.CompensateAll,
		fmt.Sprintf("fwd=%d cmp=%d", after.ForwardAll, after.CompensateAll))
}

func demoRebuildFromJournal() {
	o, _ := newOrch(saga.Config{})
	c := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
	steps := []step.Step{c.s("a", nil), {Key: "b",
		Forward: func() error { return step.NewDefiniteFailure(fail) }}}
	_, _ = o.Run("rb", steps)
	online, _ := o.State("rb")
	offline := saga.Reconstruct("rb", journalRead(o, "rb"), 2)
	same := online.Status == offline.Status && online.Records == offline.Records &&
		fmt.Sprint(online.Succeeded) == fmt.Sprint(offline.Succeeded) &&
		o.SelfCheck("rb") == nil
	check("state rebuildable from journal only", same,
		fmt.Sprintf("online=%s rebuilt=%s records=%d", online.Status, offline.Status, online.Records))
}

func demoDeterministicErrors() {
	_, err := func() (saga.State, error) {
		o, _ := newOrch(saga.Config{})
		return o.Run("e1", nil)
	}()
	empty := errors.Is(err, saga.ErrNoSteps)

	o2, _ := newOrch(saga.Config{})
	_, errDup := o2.Run("e2", []step.Step{{Key: "k"}, {Key: "k"}})

	o3, _ := newOrch(saga.Config{})
	_, errNil := o3.Run("e3", []step.Step{
		{Key: "a", Forward: func() error { return nil }},
		{Key: "b", Forward: func() error { return step.NewDefiniteFailure(fail) }},
	})

	o4, _ := newOrch(saga.Config{MaxSteps: 1})
	_, errMax := o4.Run("e4", []step.Step{{Key: "a"}, {Key: "b"}})

	_, errMissing := o4.Resume("ghost")

	ok := empty && errors.Is(errDup, saga.ErrDuplicateKey) &&
		errors.Is(errNil, saga.ErrNilCompensate) &&
		errors.Is(errMax, saga.ErrMaxSteps) &&
		errors.Is(errMissing, saga.ErrInstanceNotFound)
	check("deterministic errors distinct (empty/dup/nil/max/missing)", ok, "")
}

func demoConcurrentIsolation() {
	const N = 20
	o, _ := newOrch(saga.Config{MaxSteps: 10})
	type planT struct {
		steps []step.Step
		r     *rec
		fail  bool
	}
	plans := make([]planT, N)
	for i := 0; i < N; i++ {
		r := &rec{fwd: map[string]int{}, cmp: map[string]int{}, ord: new([]string)}
		ps := []step.Step{r.s("a", nil), r.s("b", nil), r.s("c", nil)}
		failInst := i%2 == 0
		if failInst {
			ps = append(ps, step.Step{Key: "d",
				Forward: func() error { return step.NewDefiniteFailure(fail) }})
		} else {
			ps = append(ps, r.s("d", nil))
		}
		plans[i] = planT{ps, r, failInst}
	}
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			id := fmt.Sprintf("iso-%d", i)
			_, _ = o.Run(id, plans[i].steps)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
	ok := true
	for i := 0; i < N; i++ {
		id := fmt.Sprintf("iso-%d", i)
		if err := o.SelfCheck(id); err != nil {
			ok = false
		}
		calls, _ := o.Calls(id)
		if calls.ForwardAll != 4 {
			ok = false
		}
		want := 0
		if plans[i].fail {
			want = 3
		}
		if calls.CompensateAll != want {
			ok = false
		}
	}
	check("concurrent isolated instances", ok, "N=20 each fwd=4, cmp=0 or 3")
}

func demoReadCounts() {
	line := func(records int) string {
		o, _ := newOrch(saga.Config{MaxJournalRecords: records + 1})
		id := "big"
		n := records / 2
		var steps []step.Step
		for i := 0; i < n; i++ {
			k := fmt.Sprintf("k%d", i)
			steps = append(steps, step.Step{Key: k,
				Forward: func() error { return nil }, Compensate: func() error { return nil }})
			_, _ = journalAppend(o, id, i, k, journal.Forward, journal.Success)
		}
		for i := n - 1; i >= 0; i-- {
			k := fmt.Sprintf("k%d", i)
			_, _ = journalAppend(o, id, i, k, journal.Compensate, journal.Success)
		}
		registerForDemo(o, id, steps)
		if _, err := o.Resume(id); err != nil {
			return "err:" + err.Error()
		}
		return fmt.Sprintf("len=%d reads=%d", records, resumeReads(o))
	}
	a, b := line(100), line(10000)
	check("Resume reads O(1) in journal length", a == "len=100 reads=1" && b == "len=10000 reads=1",
		a+" | "+b)
}

func journalRead(o *saga.Orchestrator, id string) []journal.Record {
	return demoJournal(o).Read(id)
}
