package main

import (
	"errors"
	"fmt"
	"sync"

	rollback "ontology"
)

var pass, fail int

func report(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

// addStep is a forward add(id,+delta) paired with an undo add(id,-delta).
func addStep(store *rollback.Store, id string, delta int) rollback.StepDef {
	return rollback.StepDef{
		Action: func() error {
			_, err := store.Add(id, delta)
			return err
		},
		Compensation: func() error {
			_, err := store.Add(id, -delta)
			return err
		},
	}
}

func main() {
	demoAllSuccess()
	demoMidFailure()
	demoCompensationFailure()
	demoCompensationPanic()
	demoConcurrentRollback()

	total := pass + fail
	if fail == 0 {
		fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	} else {
		fmt.Printf("TOTAL %d/%d OK (%d FAIL)\n", pass, total, fail)
	}
}

func demoAllSuccess() {
	store := rollback.NewStore()
	u := rollback.NewUnit(store)
	err := u.Run(addStep(store, "a", 1), addStep(store, "b", 2))
	report(err == nil && len(u.Trace()) == 0 && store.Get("a") == 1 && store.Get("b") == 2,
		"all success: committed, trace=%v (no compensation)", u.Trace())
}

func demoMidFailure() {
	store := rollback.NewStore()
	u := rollback.NewUnit(store)
	for _, def := range []rollback.StepDef{addStep(store, "a", 1), addStep(store, "b", 2), addStep(store, "c", 3)} {
		_ = u.Step(def.Action, def.Compensation)
	}
	err := u.Step(func() error { return errors.New("step4 boom") }, func() error { return nil })
	report(err != nil && fmt.Sprint(u.Trace()) == "[3 2 1]" && store.Get("a") == 0 && store.Get("b") == 0 && store.Get("c") == 0,
		"mid failure: trace=%v, counters restored to 0 0 0", u.Trace())
}

func demoCompensationFailure() {
	store := rollback.NewStore()
	u := rollback.NewUnit(store)
	compErr := errors.New("undo exploded")
	for i := 1; i <= 3; i++ {
		step := i
		comp := func() error { return nil }
		if step == 2 {
			comp = func() error { return compErr }
		}
		_ = u.Step(func() error { return nil }, comp)
	}
	err := u.Rollback()

	var agg *rollback.AggregateError
	aggregated := errors.As(err, &agg)
	continued := fmt.Sprint(u.Trace()) == "[3 2 1]"
	reason := aggregated && errors.Is(agg.Step(2), compErr)
	_, writeErr := store.Add("x", 1)
	writeRejected := rollback.IsPolluted(writeErr)

	var pe *rollback.PollutionError
	pollutionAt2 := errors.As(writeErr, &pe) && pe.Step == 2
	report(aggregated && continued && reason && writeRejected && pollutionAt2,
		"comp fail: continue=%v aggregated=%v writeRejected=%v pollutionAt=step%d",
		continued, reason, writeRejected, store.PollutionStep())
}

func demoCompensationPanic() {
	store := rollback.NewStore()
	u := rollback.NewUnit(store)
	for i := 1; i <= 3; i++ {
		step := i
		comp := func() error { return nil }
		if step == 2 {
			comp = func() error { panic("undo panic") }
		}
		_ = u.Step(func() error { return nil }, comp)
	}
	err := u.Rollback()

	var agg *rollback.AggregateError
	_ = errors.As(err, &agg)
	var pe *rollback.CompensationPanic
	caught := agg != nil && errors.As(agg.Step(2), &pe) && pe.Value == "undo panic"
	continued := fmt.Sprint(u.Trace()) == "[3 2 1]"
	report(caught && continued,
		"comp panic: caught=%q, later compensations ran, trace=%v", peValue(pe), u.Trace())
}

func demoConcurrentRollback() {
	store := rollback.NewStore()
	u := rollback.NewUnit(store)
	var mu sync.Mutex
	rec := make([]int, 0)
	for i := 1; i <= 6; i++ {
		step := i
		_ = u.Step(func() error { return nil }, func() error {
			mu.Lock()
			rec = append(rec, step)
			mu.Unlock()
			return nil
		})
	}

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = u.Rollback()
		}()
	}
	close(start)
	wg.Wait()

	noDup := len(u.Trace()) == 6 && len(rec) == 6
	report(noDup,
		"concurrent rollback x%d: trace=%v, each compensation exactly once", n, u.Trace())
}

func peValue(pe *rollback.CompensationPanic) any {
	if pe == nil {
		return nil
	}
	return pe.Value
}
