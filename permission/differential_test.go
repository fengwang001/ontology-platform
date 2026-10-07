package permission

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

type permissionAPI interface {
	Submit(change Change) (SubmitResult, error)
	Withdraw(request WithdrawRequest) error
	Decide(query Query) (QueryResult, error)
	AuditLog() []AuditRecord
}

var _ permissionAPI = (*Store)(nil)
var _ permissionAPI = (*NaiveReference)(nil)

type generatedChange struct {
	change    Change
	effective int
}

func normalizeResult(result QueryResult) QueryResult {
	copyResult := result
	copyResult.ExaminedChangeID = append([]string(nil), result.ExaminedChangeID...)
	sort.Strings(copyResult.ExaminedChangeID)
	return copyResult
}

func errorCodeValue(err error) string {
	if err == nil {
		return ""
	}
	if ruleErr, ok := err.(*RuleError); ok {
		return string(ruleErr.Code)
	}
	return err.Error()
}

func TestRandomDifferentialAgainstNaiveReference(t *testing.T) {
	const seeds = 120
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed-%03d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			actual := NewStore()
			reference := NewNaiveReference()
			var accepted []generatedChange
			var submittedHistory []Change
			subjects := []string{"alice", "bob", "carol"}
			labels := []string{"read", "write", "admin"}

			for operation := 0; operation < 120; operation++ {
				action := random.Intn(10)
				switch {
				case action < 6 || len(accepted) < 2:
					subject := subjects[random.Intn(len(subjects))]
					label := labels[random.Intn(len(labels))]
					submitted := random.Intn(12)
					effective := submitted + random.Intn(5)
					id := fmt.Sprintf("%03d-%03d", seed, operation)
					change := Change{
						ID:          id,
						Subject:     subject,
						Label:       label,
						SubmittedAt: testTime(submitted),
						EffectiveAt: testTime(effective),
					}

					targets := make([]generatedChange, 0)
					for _, candidate := range accepted {
						if candidate.change.Subject == subject && candidate.change.Label == label &&
							!candidate.change.SubmittedAt.After(change.SubmittedAt) && candidate.effective <= effective {
							targets = append(targets, candidate)
						}
					}
					if len(targets) > 0 && random.Intn(3) == 0 {
						target := targets[random.Intn(len(targets))]
						change.Kind = Revoke
						change.RevokesID = target.change.ID
						change.Decision = ""
					} else {
						change.Kind = Put
						change.Decision = []Decision{Allow, Deny}[random.Intn(2)]
					}

					actualResult, actualErr := actual.Submit(change)
					referenceResult, referenceErr := reference.Submit(change)
					if errorCodeValue(actualErr) != errorCodeValue(referenceErr) {
						t.Fatalf("submit %s error mismatch: actual=%v reference=%v", id, actualErr, referenceErr)
					}
					if actualErr == nil {
						if actualResult.Accepted != referenceResult.Accepted || actualResult.OrderKey != referenceResult.OrderKey {
							t.Fatalf("submit %s result mismatch: %+v != %+v", id, actualResult, referenceResult)
						}
						accepted = append(accepted, generatedChange{change: change, effective: effective})
						submittedHistory = append(submittedHistory, change)
					}
				case action < 8 && len(accepted) > 0:
					candidate := accepted[random.Intn(len(accepted))]
					withdrawn := candidate.change.SubmittedAt.Add(time.Second)
					if withdrawn.Before(testTime(0)) {
						withdrawn = testTime(0)
					}
					if random.Intn(2) == 0 && candidate.change.EffectiveAt.After(withdrawn) {
						withdrawn = candidate.change.EffectiveAt
					}
					request := WithdrawRequest{ID: candidate.change.ID, WithdrawnAt: withdrawn}
					actualErr := actual.Withdraw(request)
					referenceErr := reference.Withdraw(request)
					if errorCodeValue(actualErr) != errorCodeValue(referenceErr) {
						t.Fatalf("withdraw %s error mismatch: actual=%v reference=%v", candidate.change.ID, actualErr, referenceErr)
					}
					if actualErr == nil {
						for index := range accepted {
							if accepted[index].change.ID == candidate.change.ID {
								accepted[index] = accepted[len(accepted)-1]
								accepted = accepted[:len(accepted)-1]
								break
							}
						}
					}
				default:
					subject := subjects[random.Intn(len(subjects))]
					label := labels[random.Intn(len(labels))]
					at := testTime(random.Intn(14))
					query := Query{Subject: subject, Label: label, At: at}
					actualResult, actualErr := actual.Decide(query)
					referenceResult, referenceErr := reference.Decide(query)
					if errorCodeValue(actualErr) != errorCodeValue(referenceErr) {
						t.Fatalf("query %s/%s at %s error mismatch: actual=%v reference=%v",
							subject, label, at, actualErr, referenceErr)
					}
					if actualErr == nil {
						actualNormalized := normalizeResult(actualResult)
						referenceNormalized := normalizeResult(referenceResult)
						if actualNormalized.Decision != referenceNormalized.Decision ||
							actualNormalized.WinningChangeID != referenceNormalized.WinningChangeID ||
							len(actualNormalized.ExaminedChangeID) != len(referenceNormalized.ExaminedChangeID) {
							for _, loggedChange := range submittedHistory {
								if loggedChange.Subject == subject && loggedChange.Label == label {
									t.Logf("change id=%s kind=%s decision=%s revokes=%s submitted=%d effective=%d",
										loggedChange.ID, loggedChange.Kind, loggedChange.Decision, loggedChange.RevokesID,
										loggedChange.SubmittedAt.Second(), loggedChange.EffectiveAt.Second())
								}
							}
							t.Fatalf("query mismatch at op %d %s/%s at %s: actual=%+v reference=%+v",
								operation, subject, label, at, actualNormalized, referenceNormalized)
						}
						for index := range actualNormalized.ExaminedChangeID {
							if actualNormalized.ExaminedChangeID[index] != referenceNormalized.ExaminedChangeID[index] {
								t.Fatalf("query examined mismatch %s/%s at %s: %+v != %+v",
									subject, label, at, actualNormalized.ExaminedChangeID,
									referenceNormalized.ExaminedChangeID)
							}
						}
					}
				}
			}

			if len(actual.AuditLog()) != len(reference.AuditLog()) {
				t.Fatalf("audit call count mismatch: %d != %d", len(actual.AuditLog()), len(reference.AuditLog()))
			}
			for index, actualRecord := range actual.AuditLog() {
				referenceRecord := reference.AuditLog()[index]
				if actualRecord.Call != referenceRecord.Call ||
					actualRecord.Input != referenceRecord.Input {
					t.Fatalf("audit record %d mismatch: %+v != %+v", index, actualRecord, referenceRecord)
				}
			}
		})
	}
}
