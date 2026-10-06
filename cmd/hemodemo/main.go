// Command hemodemo runs a small end-to-end scenario exercising chair
// registration, weekly plan expansion, same-chair preference, deep
// disinfection between HBV and HCV, fault reassignment, infection status
// change and cancellation. Output is human readable.
package main

import (
	"fmt"

	"ontology/hemo"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func printTreatments(s *hemo.System, title string) {
	fmt.Printf("== %s ==\n", title)
	for _, cid := range hemo.ExportedChairIDs(s) {
		ids, err := s.ChairTreatments(cid)
		if err != nil {
			panic(err)
		}
		for _, id := range ids {
			tv, err := s.GetTreatment(id)
			if err != nil {
				panic(err)
			}
			fmt.Printf("  %s <- %s patient=%s [%d,%d) state=%s\n",
				cid, tv.ID, tv.PatientID, tv.Start, tv.End,
				tv.InfectionAtStart)
		}
	}
}

func main() {
	cfg := hemo.Config{
		RegularNegative: 30,
		RegularHBV:      40,
		RegularHCV:      40,
		RegularUnknown:  20,
		DeepDisinfect:   120,
		MinRecovery:     60,
	}
	s := hemo.NewSystem(cfg)

	must(s.RegisterChair(0, "N1", hemo.ZoneNormal, false))
	must(s.RegisterChair(0, "N2", hemo.ZoneNormal, true)) // observation
	must(s.RegisterChair(0, "I1", hemo.ZoneIsolation, false))
	must(s.RegisterChair(0, "I2", hemo.ZoneIsolation, false))
	must(s.RegisterPatient(0, "PA", hemo.InfectionNegative))
	must(s.RegisterPatient(0, "PU", hemo.InfectionUnknown))
	must(s.RegisterPatient(0, "PH", hemo.InfectionHBV))
	must(s.RegisterPatient(0, "PC", hemo.InfectionHCV))

	var monday hemoWeekdays
	monday[1] = true

	// Negative patient uses a normal chair; pending patient must land on N2.
	pA, err := s.AddPlan(1, "PA", monday, 600, 240, 0, 100000)
	must(err)
	pU, err := s.AddPlan(2, "PU", monday, 900, 200, 0, 100000)
	must(err)
	printTreatments(s, "after negative + pending plans")

	// HBV then HCV adjacent on the same isolation chair needs deep gap.
	_, err = s.AddPlan(3, "PH", monday, 600, 240, 0, 100000)
	must(err)
	if _, err := s.AddPlan(4, "PC", monday, 900, 100, 0, 100000); err != nil {
		fmt.Printf("HBV->HCV within deep gap rejected: %v\n", err)
	}

	// Fault N1 from minute 700: PA's first treatment is in progress, later
	// occurrences must be reassigned to another normal chair.
	must(s.FaultChair(700, 700, 20000, "N1"))
	printTreatments(s, "after N1 fault [700,20000)")

	// Determine PU as HBV at time 50000: future occurrences move to isolation.
	must(s.ChangeInfection(50000, 50000, "PU", hemo.InfectionHBV))
	printTreatments(s, "after PU determined HBV")

	cancelled := false
	for _, id := range pA.TreatmentIDs {
		if id == "" {
			continue
		}
		tv, err := s.GetTreatment(id)
		if err == nil && tv.Start > 50000 {
			must(s.CancelTreatment(50001, id))
			cancelled = true
			fmt.Printf("cancelled future treatment %s at %d\n", id, tv.Start)
			break
		}
	}
	if !cancelled {
		fmt.Println("no future PA occurrence to cancel")
	}
	printTreatments(s, "after cancelling one future PA occurrence")

	_ = pU
}

type hemoWeekdays [7]bool
