package audit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/audit"
	"ontology/naive"
)

// TestNaiveDifferential drives two independently written implementations with
// identical random histories and checks every replay, correction and legality
// result term by term.
func TestNaiveDifferential(t *testing.T) {
	for seed := int64(1); seed <= 24; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			svc, logger := newSvc(t)
			ref := naive.New()

			subjects := []string{"alice", "bob", "carol", "dave"}
			targets := []string{"doc1", "doc2", "doc3"}
			actions := []string{"read", "write", "admin"}

			// audit IDs existing in both systems.
			var auditIDs []string
			// Per-audit record: whether the original engine was forced and the
			// current "effective allow" as tracked through corrections.
			type track struct {
				originalAllow bool
				effective     bool
				corrLen       int
			}
			tracks := map[string]*track{}

			currentVersion := ""
			versionCounter := 0
			newVersion := func() string {
				nr := rng.Intn(5)
				rules := make([]audit.Rule, 0, nr)
				nRules := make([]naive.Rule, 0, nr)
				used := map[int]bool{}
				for i := 0; i < nr; i++ {
					oid := rng.Intn(4)
					for used[oid] {
						oid = rng.Intn(4)
					}
					used[oid] = true
					eff := audit.EffectAllow
					if rng.Intn(2) == 0 {
						eff = audit.EffectDeny
					}
					r := audit.Rule{
						ID:       fmt.Sprintf("rule-%d", oid),
						Order:    oid,
						Subjects: maybeWild(rng, subjects),
						Targets:  maybeWild(rng, targets),
						Actions:  maybeWild(rng, actions),
						Effect:   eff,
					}
					rules = append(rules, r)
					nRules = append(nRules, naive.Rule{
						ID: r.ID, Order: r.Order,
						Subjects: r.Subjects, Targets: r.Targets, Actions: r.Actions,
						Effect: string(r.Effect),
					})
				}
				versionCounter++
				id := fmt.Sprintf("v%d", versionCounter)
				parent := currentVersion
				if _, err := svc.CommitVersion(audit.RuleVersionInput{ID: id, ParentID: parent, Rules: audit.RuleSet{Rules: rules}}); err != nil {
					t.Fatalf("svc commit: %v", err)
				}
				if err := ref.CommitVersion(id, parent, nRules); err != nil {
					t.Fatalf("ref commit: %v", err)
				}
				currentVersion = id
				return id
			}

			// Initial version.
			newVersion()

			const ops = 400
			for op := 0; op < ops; op++ {
				switch rng.Intn(10) {
				case 0:
					newVersion()
				case 1, 2, 3, 4:
					in := audit.AuditInput{
						Subject:       subjects[rng.Intn(len(subjects))],
						Target:        targets[rng.Intn(len(targets))],
						Action:        actions[rng.Intn(len(actions))],
						Content:       fmt.Sprintf("req-%d", op),
						RuleVersionID: pickVersion(rng, currentVersion),
					}
					// ~30% of records use a defective engine so mismatches and
					// corrections occur naturally.
					var forced *bool
					if rng.Intn(10) < 3 {
						f := rng.Intn(2) == 0
						forced = &f
					}
					var rec audit.AuditRecord
					var err error
					if forced != nil {
						rec, err = svc.RecordAuditWithEngine(in, audit.StaticEngine{Name: "forced:v1", Answer: *forced})
					} else {
						rec, err = svc.RecordAudit(in)
					}
					nrec, nerr := ref.RecordAudit(in.Subject, in.Target, in.Action, in.Content, in.RuleVersionID, forced, "")
					if (err != nil) != (nerr != nil) {
						t.Fatalf("op %d record err mismatch: %v vs %v", op, err, nerr)
					}
					if err == nil {
						if rec.ID != nrec.ID || rec.Allow != nrec.Allow {
							t.Fatalf("op %d record mismatch: %+v vs %+v", op, rec, nrec)
						}
						auditIDs = append(auditIDs, rec.ID)
						tracks[rec.ID] = &track{originalAllow: rec.Allow, effective: rec.Allow}
					}
				case 5, 6:
					if len(auditIDs) == 0 {
						continue
					}
					id := auditIDs[rng.Intn(len(auditIDs))]
					rep, err := svc.Replay(id)
					nrep := ref.Replay(id)
					if !sameError(err, nrep.Err) {
						t.Fatalf("op %d replay err mismatch for %s: %v vs %v", op, id, err, nrep.Err)
					}
					if err == nil {
						if rep.Outcome != audit.ReplayOutcome(nrep.Outcome) ||
							rep.OriginalAllow != nrep.OriginalAllow ||
							rep.ReplayedAllow != nrep.ReplayedAllow ||
							rep.RuleVersionID != nrep.VersionID {
							t.Fatalf("op %d replay mismatch:\n svc=%+v\n ref=%+v", op, rep, nrep)
						}
					}
				case 7, 8:
					if len(auditIDs) == 0 {
						continue
					}
					id := auditIDs[rng.Intn(len(auditIDs))]
					tr := tracks[id]
					nextAllow := !tr.effective
					corrID := fmt.Sprintf("c-%s-%d", id, tr.corrLen+1)
					_, rep, err := svc.AppendCorrection(audit.AppendCorrectionInput{
						ID: corrID, AuditID: id, CorrectedAllow: nextAllow,
					})
					_, nrep, nerr := ref.AppendCorrection(corrID, id, nextAllow, "")
					if !sameError(err, nerr) {
						t.Fatalf("op %d correction err mismatch for %s: %v vs %v nrep=%+v", op, id, err, nerr, nrep)
					}
					if err == nil {
						if string(rep.Outcome) != nrep.Outcome {
							t.Fatalf("op %d correction replay basis: %s vs %s", op, rep.Outcome, nrep.Outcome)
						}
						tr.effective = nextAllow
						tr.corrLen++
					} else if err != nil && nerr == nil {
						t.Fatal("impossible")
					}
				case 9:
					if len(auditIDs) == 0 {
						continue
					}
					id := auditIDs[rng.Intn(len(auditIDs))]
					view, err := svc.Legality(id)
					nv := ref.Legality(id)
					if !sameError(err, nv.Err) {
						t.Fatalf("op %d legality err mismatch: %v vs %v", op, err, nv.Err)
					}
					if err == nil {
						if string(view.Kind) != nv.Kind ||
							view.OriginalAllow != nv.OriginalAllow ||
							view.EffectiveAllow != nv.EffectiveAllow ||
							activeID(view) != nv.ActiveID {
							t.Fatalf("op %d legality mismatch:\n svc=%+v\n ref=%+v", op, view, nv)
						}
					}
				}
			}

			// Final cross-check: every audit must agree, and both systems must
			// be internally intact.
			for _, id := range auditIDs {
				rep, err := svc.Replay(id)
				nrep := ref.Replay(id)
				if !sameError(err, nrep.Err) || (err == nil && rep.Outcome != audit.ReplayOutcome(nrep.Outcome)) {
					t.Fatalf("final replay mismatch %s: %+v/%v vs %+v", id, rep, err, nrep)
				}
				view, verr := svc.Legality(id)
				nv := ref.Legality(id)
				if !sameError(verr, nv.Err) || (verr == nil && string(view.Kind) != nv.Kind) {
					t.Fatalf("final legality mismatch %s: %+v vs %+v", id, view, nv)
				}
			}
			if problems := svc.VerifyAll(); len(problems) != 0 {
				t.Fatalf("svc integrity problems: %v", problems)
			}
			if len(logger.Entries()) == 0 {
				t.Fatal("no calls were logged")
			}
		})
	}
}

func activeID(v audit.LegalityView) string {
	if v.ActiveCorrection == nil {
		return ""
	}
	return v.ActiveCorrection.ID
}

func pickVersion(rng *rand.Rand, current string) string {
	// Bias heavily towards the current version, occasionally replay against an
	// older one by id family (naive and svc share ids).
	if rng.Intn(5) != 0 {
		return current
	}
	n := 1
	for k := range current {
		if current[k] == 'v' {
			n = atoiSmall(current[k+1:])
			break
		}
	}
	if n <= 1 {
		return current
	}
	return fmt.Sprintf("v%d", 1+rng.Intn(n))
}

func atoiSmall(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func maybeWild(rng *rand.Rand, pool []string) []string {
	switch rng.Intn(3) {
	case 0:
		return nil
	case 1:
		return []string{pool[rng.Intn(len(pool))]}
	default:
		return append([]string(nil), pool...)
	}
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errorClass(a) == errorClass(b)
}

func errorClass(err error) string {
	sentinels := []struct {
		sentinel error
		class    string
	}{
		{audit.ErrAuditNotFound, "audit_missing"},
		{audit.ErrVersionNotFound, "version_missing"},
		{audit.ErrVersionTampered, "version_tampered"},
		{audit.ErrAuditTampered, "audit_tampered"},
		{audit.ErrCorrectionTargetMissing, "target_missing"},
		{audit.ErrCorrectionTampered, "correction_tampered"},
		{audit.ErrCorrectionConflict, "conflict"},
		{audit.ErrReplayMatch, "match"},
		{audit.ErrInvalidInput, "invalid"},
		{audit.ErrAlreadyExists, "exists"},
		{naive.ErrAuditNotFound, "audit_missing"},
		{naive.ErrVersionNotFound, "version_missing"},
		{naive.ErrVersionTampered, "version_tampered"},
		{naive.ErrAuditTampered, "audit_tampered"},
		{naive.ErrCorrectionTargetMissing, "target_missing"},
		{naive.ErrCorrectionTampered, "correction_tampered"},
		{naive.ErrCorrectionConflict, "conflict"},
		{naive.ErrReplayMatch, "match"},
		{naive.ErrInvalidInput, "invalid"},
		{naive.ErrAlreadyExists, "exists"},
	}
	for _, pair := range sentinels {
		if errors.Is(err, pair.sentinel) {
			return pair.class
		}
	}
	return err.Error()
}
