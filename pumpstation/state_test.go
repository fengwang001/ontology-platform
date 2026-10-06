package pumpstation

import (
	"errors"
	"sync"
	"testing"
)

func TestFaultAndMaintenanceTransitions(t *testing.T) {
	controller, _ := New(testConfig())
	eval := report(t, controller, 100, 200)
	if len(eval.Actions) != 3 {
		t.Fatalf("overflow start: %#v", eval.Actions)
	}

	change, err := controller.SetFault(101, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FAULT input={t:101 pump:2 fault:true} output=%+v basis=%q", change, change.Reason)
	if len(change.Actions) != 1 || change.Actions[0].PumpID != 2 || change.Actions[0].Reason != ReasonFault {
		t.Fatalf("faulted running pump must stop immediately: %#v", change.Actions)
	}
	snapshot := controller.Snapshot()
	if snapshot.Target != 2 || snapshot.AvailablePumps != 2 || snapshot.RunningPumps != 2 {
		t.Fatalf("unexpected snapshot after fault: %+v", snapshot)
	}

	_, err = controller.SetMaintenance(102, 2, true)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("faulted pump cannot be maintained, got %v", err)
	}
	change, err = controller.SetFault(103, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RECOVER input={t:103 pump:2} output=%+v basis=%q", change, change.Reason)
	if snapshot := controller.Snapshot(); snapshot.Pumps[1].StoppedAt != 103 {
		t.Fatalf("recovery must reset stop timer at 103, got %d", snapshot.Pumps[1].StoppedAt)
	}

	change, err = controller.SetMaintenance(104, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MAINTAIN input={t:104 pump:1} output=%+v basis=%q", change, change.Reason)
	if len(change.Actions) != 0 {
		t.Fatalf("pump still owes minimum run time, got %#v", change.Actions)
	}
	if snapshot := controller.Snapshot(); !snapshot.Pumps[0].Running || snapshot.AvailablePumps != 2 || snapshot.RunningPumps != 2 {
		t.Fatalf("maintenance pump must remain running but unavailable: %+v", snapshot)
	}

	eval = report(t, controller, 109, 70)
	if len(eval.Actions) != 0 {
		t.Fatalf("one second before minimum run must not stop: %#v", eval.Actions)
	}
	eval = report(t, controller, 110, 70)
	if len(eval.Actions) != 1 || eval.Actions[0].PumpID != 1 || eval.Actions[0].Reason != ReasonMaintenanceStop {
		t.Fatalf("maintenance pump should stop exactly at minimum run, got %#v (%s)", eval.Actions, eval.Reason)
	}

	change, err = controller.SetFault(111, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if change.State != PumpFaulted || len(change.Actions) != 0 {
		t.Fatalf("maintenance fault conversion must be immediate, got %+v", change)
	}
}

func TestMaintenanceDuringDryLockStopsImmediately(t *testing.T) {
	controller, _ := New(testConfig())
	report(t, controller, 100, 140)
	report(t, controller, 101, 10)
	change, err := controller.SetMaintenance(102, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if change.State != PumpMaintenance || len(change.Actions) != 0 {
		t.Fatalf("maintenance must take immediate effect on the already dry-stopped pump, got %+v", change)
	}
}

func TestRejectionOrderAndAtomicity(t *testing.T) {
	controller, _ := New(testConfig())
	report(t, controller, 10, 100)

	before := controller.Snapshot()
	_, err := controller.ReportLevel(9, -1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument precedes time rollback, got %v", err)
	}
	_, err = controller.ReportLevel(9, 50)
	if !errors.Is(err, ErrTimeRollback) {
		t.Fatalf("time rollback precedes pump lookup, got %v", err)
	}
	_, err = controller.SetFault(9, 999, true)
	if !errors.Is(err, ErrTimeRollback) {
		t.Fatalf("time rollback precedes pump missing, got %v", err)
	}
	_, err = controller.SetFault(11, 999, true)
	if !errors.Is(err, ErrPumpNotFound) {
		t.Fatalf("missing pump precedes state, got %v", err)
	}
	_, err = controller.SetFault(12, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = controller.SetFault(13, 1, true)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate fault must be state error, got %v", err)
	}
	after := controller.Snapshot()
	if after.Time != 12 || after.Target != before.Target || after.RunningPumps != before.RunningPumps {
		t.Fatalf("rejected operations changed state: before=%+v after=%+v", before, after)
	}
}

func TestConcurrentOperationsSerialize(t *testing.T) {
	controller, _ := New(testConfig())
	var wait sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			_, err := controller.ReportLevel(int64(100+i), 100)
			if err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wait.Wait()
	close(errs)
	var accepted int
	for err := range errs {
		if !errors.Is(err, ErrTimeRollback) {
			t.Fatalf("only time rollbacks are expected from serial losers, got %v", err)
		}
	}
	_ = accepted
	if snapshot := controller.Snapshot(); snapshot.Time != 131 {
		t.Fatalf("exactly one operation per timestamp should serialize; time=%d", snapshot.Time)
	}
}
