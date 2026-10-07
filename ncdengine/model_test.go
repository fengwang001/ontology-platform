package ncdengine

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveTerm struct {
	vehicle   string
	start     int
	level     int
	protected bool
}

type naiveClaim struct {
	id        string
	vehicle   string
	day       int
	liability int
	deleted   bool
}

type naiveDelta struct {
	claimID string
	start   int
	amount  int
	deleted bool
}

type naiveModel struct {
	cfg    Config
	day    int
	terms  []naiveTerm
	claims []naiveClaim
	deltas []naiveDelta
}

func (m *naiveModel) vehicleAt(day int) string {
	for _, term := range m.terms {
		if day >= term.start && day < term.start+365 {
			return term.vehicle
		}
	}
	return ""
}

func (m *naiveModel) activeAt(day int) bool {
	last := m.terms[len(m.terms)-1]
	return day < last.start+365+m.cfg.RenewalGraceDays
}

func (m *naiveModel) rebuild(changedID string, deleted bool) {
	old := append([]naiveTerm(nil), m.terms...)
	for i := 1; i < len(m.terms); i++ {
		previous := m.terms[i-1]
		if m.terms[i].start != previous.start+365 {
			m.terms[i].level = 0
			continue
		}
		claims := make([]Claim, 0)
		for _, claim := range m.claims {
			if !claim.deleted && claim.day >= previous.start && claim.day < previous.start+365 {
				claims = append(claims, Claim{ID: claim.id, AccidentDay: claim.day, Liability: claim.liability})
			}
		}
		m.terms[i].level = nextLevel(previous.level, claims, previous.protected, m.cfg)
		if changedID != "" && m.terms[i].level < old[i].level {
			m.deltas = append(m.deltas, naiveDelta{
				claimID: changedID,
				start:   m.terms[i].start,
				amount:  m.cfg.Premiums[m.terms[i].level] - m.cfg.Premiums[old[i].level],
				deleted: deleted,
			})
		}
	}
}

func (m *naiveModel) renew(vehicle string, day int) error {
	if day < m.day {
		return ErrClockMovedBack
	}
	last := m.terms[len(m.terms)-1]
	if vehicle != last.vehicle || !withinRenewalWindow(last.start+365, day, m.cfg.RenewalGraceDays) {
		return ErrOutsideRenewalWindow
	}
	m.day = day
	claims := make([]Claim, 0)
	for _, claim := range m.claims {
		if !claim.deleted && claim.day >= last.start && claim.day < last.start+365 {
			claims = append(claims, Claim{ID: claim.id, AccidentDay: claim.day, Liability: claim.liability})
		}
	}
	level := nextLevel(last.level, claims, last.protected, m.cfg)
	m.terms = append(m.terms, naiveTerm{vehicle: vehicle, start: last.start + 365, level: level})
	return nil
}

func (m *naiveModel) protect(vehicle string, day int) error {
	if day < m.day {
		return ErrClockMovedBack
	}
	index := -1
	for i := range m.terms {
		if day >= m.terms[i].start && day < m.terms[i].start+365 && m.terms[i].vehicle == vehicle {
			index = i
		}
	}
	if index < 0 {
		return ErrOutsideRenewalWindow
	}
	if m.terms[index].level < m.cfg.ProtectionStart {
		return ErrLevelTooLow
	}
	if m.terms[index].protected {
		return ErrProtectionPurchased
	}
	m.terms[index].protected = true
	m.day = day
	return nil
}

func (m *naiveModel) transfer(from string, to string, day int) error {
	if day < m.day {
		return ErrClockMovedBack
	}
	for i := range m.terms {
		if day >= m.terms[i].start && day < m.terms[i].start+365 && m.terms[i].vehicle == from {
			m.terms[i].vehicle = to
			m.day = day
			return nil
		}
	}
	return ErrOutsideRenewalWindow
}

func (m *naiveModel) report(id string, vehicle string, accidentDay int, liability int, reportDay int) error {
	if reportDay < m.day {
		return ErrClockMovedBack
	}
	for _, claim := range m.claims {
		if claim.id == id && !claim.deleted {
			return ErrClaimExists
		}
	}
	if m.vehicleAt(accidentDay) != vehicle {
		return ErrAccidentUncovered
	}
	m.day = reportDay
	m.claims = append(m.claims, naiveClaim{id: id, vehicle: vehicle, day: accidentDay, liability: liability})
	m.rebuild(id, false)
	return nil
}

func (m *naiveModel) delete(id string, day int) error {
	if day < m.day {
		return ErrClockMovedBack
	}
	index := -1
	for i := range m.claims {
		if m.claims[i].id == id && !m.claims[i].deleted {
			index = i
		}
	}
	if index < 0 {
		return ErrClaimNotFound
	}
	m.day = day
	m.claims[index].deleted = true
	m.rebuild(id, true)
	return nil
}

func sameError(left error, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Error() == right.Error()
}

func TestRandomDifferentialAgainstNaiveReplay(t *testing.T) {
	t.Log("输入/输出/依据：随机续保、事故、保护、转移、迟报与撤销；朴素模型按完整历史逐年重放")
	for seed := int64(1); seed <= 50; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := testConfig()
			engine := NewEngine(0)
			if _, err := engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v0", StartDay: 0, Config: cfg}); err != nil {
				t.Fatal(err)
			}
			model := &naiveModel{cfg: cfg, day: 0, terms: []naiveTerm{{vehicle: "v0", start: 0}}}
			vehicle := "v0"
			activeClaims := []string{}

			for step := 0; step < 90; step++ {
				day := model.day + rng.Intn(80)
				switch rng.Intn(7) {
				case 0:
					_, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: vehicle, Day: day})
					modelErr := model.renew(vehicle, day)
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d renew engine=%v model=%v", seed, step, err, modelErr)
					}
					if err == nil {
						t.Logf("输入 续保 day=%d vehicle=%s；输出 level=%d；依据=上一年度有责次数", day, vehicle, model.terms[len(model.terms)-1].level)
					}
				case 1:
					id := fmt.Sprintf("%d-%d", seed, step)
					accidentDay := rng.Intn(max(1, day+1))
					claimVehicle := model.vehicleAt(accidentDay)
					if claimVehicle == "" {
						continue
					}
					liability := rng.Intn(101)
					err := engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: claimVehicle, ClaimID: id, AccidentDay: accidentDay, Liability: liability, ReportDay: day})
					modelErr := model.report(id, claimVehicle, accidentDay, liability, day)
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d report engine=%v model=%v", seed, step, err, modelErr)
					}
					if err == nil {
						activeClaims = append(activeClaims, id)
						t.Logf("输入 出险 id=%s accident=%d liability=%d report=%d；输出=%v；依据=事故日定位年度", id, accidentDay, liability, day, err)
					}
				case 2:
					if len(activeClaims) == 0 {
						continue
					}
					index := rng.Intn(len(activeClaims))
					id := activeClaims[index]
					err := engine.DeleteClaim(DeleteClaimInput{CustomerID: "c", ClaimID: id, Day: day})
					modelErr := model.delete(id, day)
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d delete engine=%v model=%v", seed, step, err, modelErr)
					}
					if err == nil {
						activeClaims = append(activeClaims[:index], activeClaims[index+1:]...)
					}
				case 3:
					err := engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: vehicle, Day: day})
					modelErr := model.protect(vehicle, day)
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d protect engine=%v model=%v", seed, step, err, modelErr)
					}
				case 4:
					to := fmt.Sprintf("t%d-%d", seed, step)
					_, err := engine.Transfer(TransferInput{CustomerID: "c", FromVehicleID: vehicle, ToVehicleID: to, Day: day})
					modelErr := model.transfer(vehicle, to, day)
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d transfer engine=%v model=%v", seed, step, err, modelErr)
					}
					if err == nil {
						vehicle = to
					}
				case 5, 6:
					newVehicle := fmt.Sprintf("n%d-%d", seed, step)
					_, err := engine.NewPolicy(NewPolicyInput{CustomerID: "c", VehicleID: newVehicle, Day: day})
					modelErr := func() error {
						if day < model.day {
							return ErrClockMovedBack
						}
						if model.activeAt(day) {
							return ErrActivePolicyExists
						}
						model.day = day
						model.terms = append(model.terms, naiveTerm{vehicle: newVehicle, start: day})
						return nil
					}()
					if !sameError(err, modelErr) {
						t.Fatalf("seed=%d step=%d new engine=%v model=%v", seed, step, err, modelErr)
					}
					if err == nil {
						vehicle = newVehicle
						activeClaims = nil
					}
				}
				assertMatchesNaive(t, engine, model, seed, step)
			}
		})
	}
}

func assertMatchesNaive(t *testing.T, engine *Engine, model *naiveModel, seed int64, step int) {
	t.Helper()
	terms, err := engine.Terms("c")
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != len(model.terms) {
		t.Fatalf("seed=%d step=%d terms engine=%d model=%d", seed, step, len(terms), len(model.terms))
	}
	for i, modelTerm := range model.terms {
		if terms[i].RenewalLevel != modelTerm.level || terms[i].VehicleID != modelTerm.vehicle || terms[i].StartDay != modelTerm.start {
			t.Fatalf("seed=%d step=%d term=%d engine=%+v model=%+v", seed, step, i, terms[i], modelTerm)
		}
	}
	deltas, _ := engine.PremiumDeltas("c")
	if len(deltas) != len(model.deltas) {
		t.Fatalf("seed=%d step=%d deltas engine=%d model=%d", seed, step, len(deltas), len(model.deltas))
	}
	for i, modelDelta := range model.deltas {
		if deltas[i].ClaimID != modelDelta.claimID || deltas[i].StartDay != modelDelta.start || deltas[i].Amount != modelDelta.amount || deltas[i].DeletedClaim != modelDelta.deleted {
			t.Fatalf("seed=%d step=%d delta=%d engine=%+v model=%+v", seed, step, i, deltas[i], modelDelta)
		}
	}
}
