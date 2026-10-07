package subro_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/naivemodel"
	"ontology/subro"
)

func kindOf(err error) string {
	if err == nil {
		return "<nil>"
	}
	for k := subro.ErrInvalidParam; k <= subro.ErrAlreadyWaived; k++ {
		if subro.IsKind(err, k) {
			return k.String()
		}
	}
	return fmt.Sprintf("<unknown: %v>", err)
}

// op is one randomized operation applied to both implementations.
type op struct {
	desc string
	sys  func(s *subro.System) ([]subro.Adjustment, error)
	naiv func(m *naivemodel.Model) ([]subro.Adjustment, error)
}

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	const (
		seeds       = 30
		opsPerSeed  = 200
		maxCases    = 3
		maxLoss     = 2000
		maxDeadline = 30
	)
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sys := subro.NewSystem()
		naiv := naivemodel.New()
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			var caseIDs []string
			var now int64

			// Initial registrations (identical for both sides).
			for i := 0; i < 1+rng.Intn(maxCases); i++ {
				id := fmt.Sprintf("case-%d", i)
				totalLoss := int64(rng.Intn(maxLoss + 1))
				paid := int64(0)
				if totalLoss > 0 {
					paid = int64(rng.Intn(int(totalLoss) + 1))
				}
				deadline := int64(rng.Intn(maxDeadline + 1))
				ratioBP := int64(rng.Intn(10001))
				if err := sys.RegisterCase(now, id, totalLoss, paid, deadline, ratioBP); err != nil {
					t.Fatalf("system register: %v", err)
				}
				if err := naiv.RegisterCase(now, id, totalLoss, paid, deadline, ratioBP); err != nil {
					t.Fatalf("model register: %v", err)
				}
				caseIDs = append(caseIDs, id)
				t.Logf("register id=%s totalLoss=%d paid=%d deadline=%d ratioBP=%d now=%d",
					id, totalLoss, paid, deadline, ratioBP, now)
			}

			pickCase := func() string {
				if rng.Intn(10) == 0 {
					return "ghost"
				}
				return caseIDs[rng.Intn(len(caseIDs))]
			}
			advanceClock := func() int64 {
				if rng.Intn(10) == 0 {
					return now - int64(rng.Intn(4)) // may roll back or go negative
				}
				now += int64(rng.Intn(3))
				return now
			}

			for i := 0; i < opsPerSeed; i++ {
				var o op
				switch rng.Intn(5) {
				case 0, 1: // recover (most common)
					id, n := pickCase(), advanceClock()
					gross := int64(rng.Intn(1501))
					expense := int64(0)
					if gross > 0 {
						expense = int64(rng.Intn(int(gross) + 1))
					}
					if rng.Intn(10) == 0 {
						expense = gross + 1 + int64(rng.Intn(5)) // invalid
					}
					o = op{
						desc: fmt.Sprintf("recover id=%s now=%d gross=%d expense=%d", id, n, gross, expense),
						sys:  func(s *subro.System) ([]subro.Adjustment, error) { return s.Recover(n, id, gross, expense) },
						naiv: func(m *naivemodel.Model) ([]subro.Adjustment, error) { return m.Recover(n, id, gross, expense) },
					}
				case 2: // adjust ratio
					id, n := pickCase(), advanceClock()
					ratioBP := int64(rng.Intn(10001))
					if rng.Intn(10) == 0 {
						ratioBP = 10001 + int64(rng.Intn(100)) // invalid
					}
					o = op{
						desc: fmt.Sprintf("adjust_ratio id=%s now=%d ratioBP=%d", id, n, ratioBP),
						sys:  func(s *subro.System) ([]subro.Adjustment, error) { return s.AdjustRatio(n, id, ratioBP) },
						naiv: func(m *naivemodel.Model) ([]subro.Adjustment, error) { return m.AdjustRatio(n, id, ratioBP) },
					}
				case 3: // supplement
					id, n := pickCase(), advanceClock()
					amount := int64(rng.Intn(2500)) // may exceed the total loss
					o = op{
						desc: fmt.Sprintf("supplement id=%s now=%d amount=%d", id, n, amount),
						sys:  func(s *subro.System) ([]subro.Adjustment, error) { return s.SupplementPayment(n, id, amount) },
						naiv: func(m *naivemodel.Model) ([]subro.Adjustment, error) { return m.SupplementPayment(n, id, amount) },
					}
				default: // waive
					id, n := pickCase(), advanceClock()
					o = op{
						desc: fmt.Sprintf("waive id=%s now=%d", id, n),
						sys:  func(s *subro.System) ([]subro.Adjustment, error) { return s.Waive(n, id) },
						naiv: func(m *naivemodel.Model) ([]subro.Adjustment, error) { return m.Waive(n, id) },
					}
				}

				sysAdjs, sysErr := o.sys(sys)
				naivAdjs, naivErr := o.naiv(naiv)

				if kindOf(sysErr) != kindOf(naivErr) {
					t.Fatalf("op %d %s: error mismatch: system=%s model=%s",
						i, o.desc, kindOf(sysErr), kindOf(naivErr))
				}
				if !reflect.DeepEqual(sysAdjs, naivAdjs) {
					t.Fatalf("op %d %s: adjustments mismatch:\nsystem=%v\nmodel =%v",
						i, o.desc, sysAdjs, naivAdjs)
				}

				// Log input, output and the decision basis.
				if sysErr != nil {
					t.Logf("op %d %s -> rejected: %s", i, o.desc, kindOf(sysErr))
				} else {
					t.Logf("op %d %s -> accepted, adjustments=%v", i, o.desc, adjSummary(sysAdjs))
				}
			}

			// Final state comparison with the entitlement basis logged.
			for _, id := range caseIDs {
				ss, err := sys.Snapshot(id)
				if err != nil {
					t.Fatal(err)
				}
				ns, err := naiv.Snapshot(id)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("final id=%s basis: net=%d cap=%d uncomp=%d insurerPaid=%d waived=%v "+
					"-> entitled=%+v paid=%+v",
					id, ss.NetTotal, ss.Cap, ss.Uncompensated, ss.InsurerPaid, ss.Waived,
					ss.Entitled, ss.Paid)
				if !reflect.DeepEqual(ss, ns) {
					t.Fatalf("case %s snapshot mismatch:\nsystem=%+v\nmodel =%+v", id, ss, ns)
				}
				if ss.Entitled.Sum() != ss.NetTotal {
					t.Fatalf("case %s: entitled sum %d != net total %d", id, ss.Entitled.Sum(), ss.NetTotal)
				}
				if ss.Paid != ss.Entitled {
					t.Fatalf("case %s: paid %+v != entitled %+v", id, ss.Paid, ss.Entitled)
				}
				if !reflect.DeepEqual(sys.Adjustments(id), naiv.Adjustments(id)) {
					t.Fatalf("case %s: full adjustment logs differ", id)
				}
			}
		})
	}
}
