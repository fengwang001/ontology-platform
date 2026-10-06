package fuzz_test

import (
	"fmt"
	"math/rand"

	"ontology/hemo"
	"ontology/hemo/naive"
)

func runSeq(seqIdx, nOps int, rng *rand.Rand, logs *[]stepLog) error {
	pcfg, ncfg := bothConfigs()
	prod := hemo.NewSystem(pcfg)
	ref := naive.New(ncfg)
	g := &generator{rng: rng}

	knownChairs := map[string]bool{}
	knownPatients := map[string]bool{}

	for step := 0; step < nOps; step++ {
		o := g.next()
		var prodErr, refErr error

		switch o.Kind {
		case opRegChair:
			prodErr = prod.RegisterChair(o.Now, o.ID, pZone(o.Zone), o.Obs)
			refErr = ref.RegisterChair(o.Now, o.ID, o.Zone, o.Obs)
			if prodErr == nil {
				g.chairs = append(g.chairs, o.ID)
				knownChairs[o.ID] = true
			}
		case opRegPatient:
			prodErr = prod.RegisterPatient(o.Now, o.ID, hemo.Infection(o.Inf))
			refErr = ref.RegisterPatient(o.Now, o.ID, o.Inf)
			if prodErr == nil {
				g.patients = append(g.patients, o.ID)
				knownPatients[o.ID] = true
			}
		case opAddPlan:
			_, prodErr = prod.AddPlanWithID(o.Now, o.ID2, o.ID, o.Wk,
				o.DayStart, o.Dur, o.From, o.To)
			var pid string
			pid, refErr = ref.AddPlanWithID(o.Now, o.ID2, o.ID, o.Wk,
				o.DayStart, o.Dur, o.From, o.To)
			if prodErr == nil && pid != "" {
				g.plans = append(g.plans, pid)
			}
		case opFault:
			prodErr = prod.FaultChair(o.Now, o.At, o.Until, o.ID)
			refErr = ref.FaultChair(o.Now, o.At, o.Until, o.ID)
		case opRecover:
			prodErr = prod.RecoverChair(o.Now, o.ID)
			refErr = ref.RecoverChair(o.Now, o.ID)
		case opChange:
			prodErr = prod.ChangeInfection(o.Now, o.At, o.ID,
				hemo.Infection(o.Inf2))
			refErr = ref.ChangeInfection(o.Now, o.At, o.ID, o.Inf2)
		case opCancelTr:
			tid := treatmentOfPlan(ref, o.ID2)
			ptid := treatmentOfPlanProd(prod, o.ID2)
			if tid == "" {
				tid = "tr-missing"
			}
			if ptid == "" {
				ptid = "tr-missing"
			}
			prodErr = prod.CancelTreatment(o.Now, ptid)
			refErr = ref.CancelTreatment(o.Now, tid)
		case opCancelPlan:
			prodErr = prod.CancelPlan(o.Now, o.ID2)
			refErr = ref.CancelPlan(o.Now, o.ID2)
		}

		row := stepLog{
			Seq: seqIdx, Step: step,
			Input:     opInput(o, knownChairs, knownPatients),
			Basis:     describe(o),
			ProdError: errStr(prodErr), NaiveErr: errStr(refErr),
			ProdCode: codeProd(prodErr), NaiveCode: codeNaive(refErr),
			State: prodRows(prod), Patients: patientStates(prod),
		}
		*logs = append(*logs, row)

		if row.ProdCode != row.NaiveCode {
			return fmt.Errorf(
				"seq %d step %d (%s) code mismatch prod=%d(%v) naive=%d(%v)",
				seqIdx, step, opName(o.Kind),
				row.ProdCode, prodErr, row.NaiveCode, refErr)
		}
		if row.ProdCode == 0 {
			if d := diffRows(prodRows(prod), naiveRows(ref)); d != "" {
				return fmt.Errorf("seq %d step %d (%s) state mismatch\n%s",
					seqIdx, step, opName(o.Kind), d)
			}
		}
	}
	return nil
}
