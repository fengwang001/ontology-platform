// Command lifecycle-demo runs a scripted scenario and prints each lazy
// settlement: inputs, advanced rings and the decision basis.
package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/lifecycle"
)

func main() {
	start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	clock := lifecycle.NewManualClock(start)
	logger := &lifecycle.WriterLogger{W: os.Stdout}
	eng := lifecycle.NewEngine(clock.ClockFunc(), logger)

	must(eng.RegisterType(&lifecycle.ObjectType{
		Name: "ReviewTask",
		Transitions: []lifecycle.TransitionDef{
			{Name: "expire-review", From: "Open", To: "Pending", Trigger: lifecycle.TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "expire-escalate", From: "Pending", To: "Escalated", Trigger: lifecycle.TriggerExpiry, Duration: 20 * time.Minute},
			{Name: "close", From: "Escalated", To: "Closed", Trigger: lifecycle.TriggerAction, Actions: []string{"close"},
				Chain: []lifecycle.ChainEffect{{Link: "archive", TargetFrom: "Live", TransitionName: "freeze"}}},
		},
	}))
	must(eng.RegisterType(&lifecycle.ObjectType{
		Name: "Archive",
		Transitions: []lifecycle.TransitionDef{
			{Name: "freeze", From: "Live", To: "Frozen", Trigger: lifecycle.TriggerForced},
			{Name: "expire-purge", From: "Frozen", To: "Purged", Trigger: lifecycle.TriggerExpiry, Duration: time.Hour},
		},
	}))

	must(eng.RegisterInstance("task-1", "ReviewTask", "Open", start))
	must(eng.RegisterInstance("archive-1", "Archive", "Live", start))
	must(eng.SetLink("task-1", "archive", "archive-1"))

	fmt.Println("=== 35 minutes pass with no access: nothing runs (no scanner) ===")
	clock.Advance(35 * time.Minute)

	fmt.Println("=== first read lazily settles the whole multi-ring expiry chain ===")
	snap, err := eng.Get("task-1")
	fmt.Printf("result: state=%s enteredAt=%s err=%v\n\n", snap.State, snap.EnteredAt.Format("15:04:05"), err)

	fmt.Println("=== explicit action is judged only AFTER settlement, and chains ===")
	snap, err = eng.Act("task-1", "close")
	fmt.Printf("result: task=%s err=%v\n", snap.State, err)
	arch, _ := eng.Get("archive-1")
	fmt.Printf("result: archive=%s enteredAt=%s\n\n", arch.State, arch.EnteredAt.Format("15:04:05"))

	fmt.Println("=== clock regresses; committed rings stay committed ===")
	clock.Regress(30 * time.Minute)
	snap, err = eng.Get("task-1")
	fmt.Printf("result: state=%s (unchanged) err=%v\n\n", snap.State, err)

	fmt.Println("=== concurrent reads: expiry work happens exactly once ===")
	clock.Advance(2 * time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = eng.Get("archive-1")
		}()
	}
	wg.Wait()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
