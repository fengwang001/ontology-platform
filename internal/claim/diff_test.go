package claim_test

import (
	"errors"
	"fmt"
	"sort"
	"testing"

	"ontology/internal/claim"
	"ontology/internal/naive"
)

type rng struct{ x uint64 }

func (r *rng) n(mod uint64) int {
	r.x ^= r.x << 13
	r.x ^= r.x >> 7
	r.x ^= r.x << 17
	return int(r.x % mod)
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, claim.ErrInvalidArg):
		return "invalid"
	case errors.Is(err, claim.ErrDuplicateID):
		return "dup"
	case errors.Is(err, claim.ErrNotFound):
		return "missing"
	case errors.Is(err, claim.ErrPolicyCancelled):
		return "cancelled"
	default:
		return "other"
	}
}

func norm(res *claim.AccidentResult) string {
	if res == nil {
		return "<nil>"
	}
	pays := append([]claim.Payment(nil), res.Payments...)
	sort.Slice(pays, func(i, j int) bool { return pays[i].PolicyID < pays[j].PolicyID })
	return fmt.Sprintf("seq=%d total=%d pays=%v", res.Seq, res.TotalPaid, pays)
}

func cmpResults(t *testing.T, opDesc string, got, want *claim.AccidentResult) {
	t.Helper()
	if norm(got) != norm(want) {
		t.Fatalf("result mismatch after %s\n got %s\nwant %s", opDesc, norm(got), norm(want))
	}
}

func TestRandomDifferential(t *testing.T) {
	cases := 120
	opsPerCase := 120
	for c := 0; c < cases; c++ {
		r := &rng{x: 0x9e3779b97f4a7c15 ^ uint64(c)*0x100000001b3}
		sys := claim.NewSystem()
		ref := naive.NewModel()

		policyIDs := []string{}
		accidentIDs := []string{}
		subjects := []string{"car", "ship", "house"}

		for o := 0; o < opsPerCase; o++ {
			kind := r.n(100)
			desc := ""
			switch {
			case kind < 35:
				id := fmt.Sprintf("p%d_%d", c, len(policyIDs))
				in := claim.Policy{
					ID:         id,
					Subject:    subjects[r.n(uint64(len(subjects)))],
					Limit:      int64(1 + r.n(300)),
					Deductible: int64(r.n(40)),
					StartDay:   r.n(15),
					EndDay:     15 + r.n(15),
					Insurer:    fmt.Sprintf("I%d", r.n(4)),
					Clause:     []claim.ClauseType{claim.Ordinary, claim.Excess}[r.n(2)],
				}
				// Occasionally inject invalid fields.
				if r.n(10) == 0 {
					in.Limit = 0
				}
				desc = fmt.Sprintf("AddPolicy(%+v)", in)
				e1 := sys.AddPolicy(in)
				e2 := ref.AddPolicy(in)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("case %d op %d error class mismatch\n %s\nsys=%v ref=%v", c, o, desc, e1, e2)
				}
				if e1 == nil {
					policyIDs = append(policyIDs, id)
				}
				t.Logf("case=%d op=%d %s -> %s", c, o, desc, errClass(e1))

			case kind < 70:
				id := fmt.Sprintf("a%d_%d", c, len(accidentIDs))
				in := claim.Accident{
					ID:      id,
					Subject: subjects[r.n(uint64(len(subjects)))],
					Day:     r.n(35),
					Loss:    int64(r.n(400)),
				}
				desc = fmt.Sprintf("Register(%+v)", in)
				r1, e1 := sys.RegisterAccident(in)
				r2, e2 := ref.RegisterAccident(in)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("case %d op %d err mismatch %s sys=%v ref=%v", c, o, desc, e1, e2)
				}
				if e1 == nil {
					accidentIDs = append(accidentIDs, id)
					cmpResults(t, desc, r1, r2)
					t.Logf("case=%d op=%d %s -> %s", c, o, desc, norm(r1))
				} else {
					t.Logf("case=%d op=%d %s -> rejected:%s", c, o, desc, errClass(e1))
				}

			case kind < 85:
				if len(accidentIDs) == 0 {
					break
				}
				id := accidentIDs[r.n(uint64(len(accidentIDs)))]
				in := claim.CorrectAccidentInput{AccidentID: id, NewLoss: int64(r.n(400))}
				if r.n(12) == 0 {
					in.NewLoss = -1
				}
				desc = fmt.Sprintf("Correct(%+v)", in)
				r1, e1 := sys.CorrectAccident(in)
				r2, e2 := ref.CorrectAccident(in)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("case %d op %d err mismatch %s sys=%v ref=%v", c, o, desc, e1, e2)
				}
				if e1 == nil {
					cmpResults(t, desc, r1, r2)
				}
				t.Logf("case=%d op=%d %s -> %s %s", c, o, desc, errClass(e1), func() string {
					if r1 != nil {
						return norm(r1)
					}
					return ""
				}())

			default:
				if len(policyIDs) == 0 {
					break
				}
				id := policyIDs[r.n(uint64(len(policyIDs)))]
				in := claim.CancelPolicyInput{PolicyID: id, CancelDay: r.n(35)}
				desc = fmt.Sprintf("Cancel(%+v)", in)
				e1 := sys.CancelPolicy(in)
				e2 := ref.CancelPolicy(in)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("case %d op %d err mismatch %s sys=%v ref=%v", c, o, desc, e1, e2)
				}
				t.Logf("case=%d op=%d %s -> %s", c, o, desc, errClass(e1))
			}

			// Compare full observable state periodically and at the end.
			if o%25 != 0 && o != opsPerCase-1 {
				continue
			}
			for _, id := range accidentIDs {
				g, e1 := sys.AccidentResult(id)
				w, e2 := ref.AccidentResult(id)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("case %d: accident %s lookup err sys=%v ref=%v", c, id, e1, e2)
				}
				if e1 == nil {
					cmpResults(t, fmt.Sprintf("read %s (op %d: %s)", id, o, desc), g, w)
				}
			}
			for _, id := range policyIDs {
				g, e1 := sys.RemainingLimit(id)
				w, e2 := ref.RemainingLimit(id)
				if e1 != nil || e2 != nil || g != w {
					t.Fatalf("case %d: remaining %s sys=(%d,%v) ref=(%d,%v)", c, id, g, e1, w, e2)
				}
				if g < 0 {
					t.Fatalf("case %d: remaining of %s negative %d", c, id, g)
				}
			}
		}
	}
}
