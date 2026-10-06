package insurance

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// 性能证明：更正开销只与受影响事故数相关。
// 标的车有 n 起事故，标的书有 5000 起事故；更正车的首起事故，
// 重算计数必须恰好等于 n，与无关事故总数无关。
func TestCorrectionCostIndependentOfUnaffected(t *testing.T) {
	const carAccidents = 40
	const bookAccidents = 5000
	s := NewSystem()
	if err := s.AddPolicy(Policy{ID: "Pcar", Subject: "car", SumInsured: 1 << 40, Effective: 0, Expiry: 10000, Insurer: "i", Clause: ClauseNormal}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPolicy(Policy{ID: "Pbook", Subject: "book", SumInsured: 1 << 40, Effective: 0, Expiry: 10000, Insurer: "i", Clause: ClauseNormal}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < carAccidents; i++ {
		if _, err := s.RegisterAccident(Accident{ID: fmt.Sprintf("car-%d", i), Subject: "car", Date: int64(i), Loss: 10}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < bookAccidents; i++ {
		if _, err := s.RegisterAccident(Accident{ID: fmt.Sprintf("book-%d", i), Subject: "book", Date: int64(i), Loss: 10}); err != nil {
			t.Fatal(err)
		}
	}
	s.ResetStats()
	if err := s.CorrectAccident("car-0", 20); err != nil {
		t.Fatal(err)
	}
	got := s.Stats().RecomputedAccidents
	t.Logf("corrected car-0: recomputed=%d, car accidents=%d, unrelated book accidents=%d", got, carAccidents, bookAccidents)
	if got != carAccidents {
		t.Fatalf("recomputation must touch exactly the %d affected accidents, got %d", carAccidents, got)
	}
}

// 性能证明：登记开销只与覆盖它的保单数相关，与事故总数无关。
func TestRegistrationCostIndependentOfHistory(t *testing.T) {
	s := NewSystem()
	// 大量其他标的的保单与事故。
	for i := 0; i < 200; i++ {
		if err := s.AddPolicy(Policy{ID: fmt.Sprintf("P%d", i), Subject: "other", SumInsured: 1 << 40, Effective: 0, Expiry: 10000, Insurer: "i", Clause: ClauseNormal}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5000; i++ {
		if _, err := s.RegisterAccident(Accident{ID: fmt.Sprintf("A%d", i), Subject: "other", Date: int64(i), Loss: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// 本标的只有 3 张保单。
	for i := 0; i < 3; i++ {
		if err := s.AddPolicy(Policy{ID: fmt.Sprintf("Q%d", i), Subject: "mine", SumInsured: 100, Effective: 0, Expiry: 10000, Insurer: "i", Clause: ClauseNormal}); err != nil {
			t.Fatal(err)
		}
	}
	s.ResetStats()
	if _, err := s.RegisterAccident(Accident{ID: "target", Subject: "mine", Date: 1, Loss: 1}); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().CoverageChecks; got != 3 {
		t.Fatalf("registration must only inspect the 3 same-subject policies, got %d", got)
	}
	s.ResetStats()
	if _, err := s.RegisterAccident(Accident{ID: "unknown-subject", Subject: "void", Date: 1, Loss: 1}); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().CoverageChecks; got != 0 {
		t.Fatalf("registration on a policy-less subject must do 0 coverage checks, got %d", got)
	}
	t.Logf("registration coverage checks stay at subject-policy count regardless of 5000 prior accidents")
}

// 并发：所有操作可并发调用，且任何保单累计赔付不超过保额、
// 任何事故总赔付不超过损失额、分赔付之和等于总赔付。
func TestConcurrentOps(t *testing.T) {
	s := NewSystem()
	const subjects = 4
	const goroutines = 8
	const opsPerGoroutine = 300

	for sub := 0; sub < subjects; sub++ {
		for i := 0; i < 3; i++ {
			clause := ClauseNormal
			if i == 2 {
				clause = ClauseExcess
			}
			if err := s.AddPolicy(Policy{
				ID: fmt.Sprintf("P-%d-%d", sub, i), Subject: fmt.Sprintf("s%d", sub),
				SumInsured: 500, Deductible: int64(i * 3), Effective: 0, Expiry: 1000,
				Insurer: "ins", Clause: clause,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	accidentIDs := make([][]string, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g + 1)))
			var mine []string
			for op := 0; op < opsPerGoroutine; op++ {
				sub := rng.Intn(subjects)
				id := fmt.Sprintf("g%d-op%d", g, op)
				switch rng.Intn(5) {
				case 0, 1, 2:
					if _, err := s.RegisterAccident(Accident{ID: id, Subject: fmt.Sprintf("s%d", sub), Date: int64(rng.Intn(50)), Loss: int64(rng.Intn(200))}); err == nil {
						mine = append(mine, id)
					}
				case 3:
					if len(mine) > 0 {
						_ = s.CorrectAccident(mine[rng.Intn(len(mine))], int64(rng.Intn(200)))
					}
				case 4:
					_, _ = s.AccidentResultOf(id) // 并发只读查询
					_, _ = s.PolicyStatusOf(fmt.Sprintf("P-%d-0", sub))
				}
			}
			accidentIDs[g] = mine
		}(g)
	}
	wg.Wait()

	// 不变量校验。
	paidByPolicy := map[string]int64{}
	for _, ids := range accidentIDs {
		for _, id := range ids {
			res, ok := s.AccidentResultOf(id)
			if !ok {
				t.Fatalf("accident %s missing", id)
			}
			var sum int64
			for _, p := range res.Payouts {
				sum += p.Amount
				paidByPolicy[p.PolicyID] += p.Amount
			}
			if sum != res.TotalPaid {
				t.Fatalf("accident %s: payouts sum %d != total %d", id, sum, res.TotalPaid)
			}
			if res.TotalPaid > res.Loss {
				t.Fatalf("accident %s: total %d exceeds loss %d", id, res.TotalPaid, res.Loss)
			}
		}
	}
	for sub := 0; sub < subjects; sub++ {
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("P-%d-%d", sub, i)
			st, ok := s.PolicyStatusOf(id)
			if !ok {
				t.Fatalf("policy %s missing", id)
			}
			if st.TotalPaid > st.Policy.SumInsured || st.Remaining < 0 {
				t.Fatalf("policy %s exceeds sum insured: %+v", id, st)
			}
			if paidByPolicy[id] != st.TotalPaid {
				t.Fatalf("policy %s: summed payouts %d != total paid %d", id, paidByPolicy[id], st.TotalPaid)
			}
		}
	}
}

// 确定性：相同操作序列重放得到完全相同的赔付。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]AccidentResult, []PolicyStatus) {
		s := NewSystem()
		rng := rand.New(rand.NewSource(99))
		var accidentIDs, policyIDs []string
		for i := 0; i < 60; i++ {
			pid := fmt.Sprintf("P%d", i)
			clause := ClauseNormal
			if i%3 == 2 {
				clause = ClauseExcess
			}
			if err := s.AddPolicy(Policy{
				ID: pid, Subject: fmt.Sprintf("s%d", i%3), SumInsured: int64(50 + rng.Intn(200)),
				Deductible: int64(rng.Intn(20)), Effective: int64(rng.Intn(10)), Expiry: int64(20 + rng.Intn(20)),
				Insurer: "ins", Clause: clause,
			}); err == nil {
				policyIDs = append(policyIDs, pid)
			}
		}
		for i := 0; i < 200; i++ {
			aid := fmt.Sprintf("A%d", i)
			if _, err := s.RegisterAccident(Accident{ID: aid, Subject: fmt.Sprintf("s%d", rng.Intn(3)), Date: int64(rng.Intn(30)), Loss: int64(rng.Intn(150))}); err == nil {
				accidentIDs = append(accidentIDs, aid)
			}
			if i%7 == 3 && len(accidentIDs) > 0 {
				_ = s.CorrectAccident(accidentIDs[rng.Intn(len(accidentIDs))], int64(rng.Intn(150)))
			}
		}
		var results []AccidentResult
		for _, id := range accidentIDs {
			r, _ := s.AccidentResultOf(id)
			results = append(results, r)
		}
		var statuses []PolicyStatus
		for _, id := range policyIDs {
			st, _ := s.PolicyStatusOf(id)
			statuses = append(statuses, st)
		}
		return results, statuses
	}
	r1, s1 := run()
	r2, s2 := run()
	if fmt.Sprintf("%v", r1) != fmt.Sprintf("%v", r2) {
		t.Fatalf("replaying the same op sequence produced different accident results")
	}
	if fmt.Sprintf("%v", s1) != fmt.Sprintf("%v", s2) {
		t.Fatalf("replaying the same op sequence produced different policy statuses")
	}
}

func BenchmarkRegisterAccident(b *testing.B) {
	s := NewSystem()
	for i := 0; i < 10; i++ {
		_ = s.AddPolicy(Policy{ID: fmt.Sprintf("P%d", i), Subject: "car", SumInsured: 1 << 40, Effective: 0, Expiry: 100000, Insurer: "i", Clause: ClauseNormal})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.RegisterAccident(Accident{ID: fmt.Sprintf("A%d", i), Subject: "car", Date: int64(i), Loss: 100})
	}
}

func BenchmarkCorrectAccidentSuffix(b *testing.B) {
	for _, affected := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("affected=%d", affected), func(b *testing.B) {
			s := NewSystem()
			_ = s.AddPolicy(Policy{ID: "P", Subject: "car", SumInsured: 1 << 50, Effective: 0, Expiry: 1000000, Insurer: "i", Clause: ClauseNormal})
			for i := 0; i < affected; i++ {
				_, _ = s.RegisterAccident(Accident{ID: fmt.Sprintf("A%d", i), Subject: "car", Date: int64(i), Loss: 100})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = s.CorrectAccident("A0", int64(100+i%7))
			}
		})
	}
}
