package simulation

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"ontology/imaging"
)

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	if !testing.Verbose() {
		t.Log("re-run with -v to print every step input, output, and decision basis")
	}
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(9000-seed)))
			scenario := GenerateScenario(rng, 35+rng.IntN(45))
			real := imaging.NewSystem(scenario.Config)
			naive := NewNaiveSystem(scenario.Config)
			for index, op := range scenario.Operations {
				realErr, naiveErr := Apply(real, naive, op)
				realCode := imaging.CodeOf(realErr)
				naiveCode := imaging.CodeOf(naiveErr)
				basis := decisionBasis(op, real, naive)
				t.Logf("step=%d input=%+v real=%s naive=%s basis=%s", index, op, realCode, naiveCode, basis)
				if realCode != naiveCode {
					t.Fatalf("mismatch at step %d: real=%s naive=%s", index, realCode, naiveCode)
				}
				compareSnapshots(t, real, naive, op)
			}
		})
	}
}

func decisionBasis(op Operation, real *imaging.System, naive *NaiveSystem) string {
	switch op.Name {
	case "book", "reschedule", "checkin":
		if appt, exists := real.Appointment(op.ID); exists {
			naiveAppt, _ := naive.Appointment(op.ID)
			return fmt.Sprintf("status real=%s naive=%s occupancy=[%d,%d) observation_end=%d", appt.Status, naiveAppt.Status, appt.Start, appt.OccupancyEnd, appt.End+naive.config.ObservationDuration)
		}
		return "reference or candidate evaluation before state insertion"
	default:
		return "parameter/clock/reference/state validation"
	}
}

func compareSnapshots(t *testing.T, real *imaging.System, naive *NaiveSystem, op Operation) {
	t.Helper()
	if op.ID == "" {
		return
	}
	realAppt, realExists := real.Appointment(op.ID)
	naiveAppt, naiveExists := naive.Appointment(op.ID)
	if realExists != naiveExists {
		t.Fatalf("appointment existence real=%v naive=%v", realExists, naiveExists)
	}
	if realExists {
		if realAppt.Status != naiveAppt.Status ||
			realAppt.Start != naiveAppt.Start ||
			realAppt.End != naiveAppt.End ||
			realAppt.OccupancyEnd != naiveAppt.OccupancyEnd ||
			realAppt.DeviceID != naiveAppt.DeviceID ||
			realAppt.HasHydration != naiveAppt.HasHydration ||
			realAppt.HasPremed != naiveAppt.HasPremed {
			t.Fatalf("snapshot mismatch real=%+v naive=%+v", realAppt, naiveAppt)
		}
	}
}
