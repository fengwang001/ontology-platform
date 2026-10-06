package staffing

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

func callService(s *Service, op randomOp) error {
	switch op.kind {
	case "candidate":
		return s.AddCandidate(op.day, op.candidate)
	case "position":
		return s.AddPosition(op.day, Position{
			ID:        op.position,
			Level:     "L",
			MinSalary: 80,
			MaxSalary: 120,
			Total:     op.number,
		})
	case "freeze":
		return s.SetPositionStatus(op.day, op.position, PositionFrozen)
	case "open":
		return s.SetPositionStatus(op.day, op.position, PositionOpen)
	case "adjust":
		return s.AdjustHeadcount(op.day, op.position, op.number)
	case "grant":
		return s.GrantException(op.day, op.position, op.approval, op.number)
	case "issue":
		_, err := s.IssueOffer(IssueOfferInput{
			Now:                 op.day,
			OfferID:             op.offer,
			CandidateID:         op.candidate,
			PositionID:          op.position,
			Salary:              op.number,
			Deadline:            op.deadline,
			ExceptionApprovalID: op.approval,
		})
		return err
	case "respond":
		return s.RespondToOffer(op.day, op.offer, op.accept, op.start)
	case "withdraw":
		return s.WithdrawOffer(op.day, op.offer)
	case "cancel":
		return s.CancelAcceptedOffer(op.day, op.offer)
	case "onboard":
		return s.Onboard(op.day, op.offer)
	case "terminate":
		return s.TerminateEmployment(op.day, op.offer)
	default:
		return naiveError(ErrInvalidArgument)
	}
}

func randomErrorCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	if serviceErr, ok := err.(*Error); ok {
		return serviceErr.Code
	}
	return "non_staffing_error"
}

func generateRandomOp(rng *rand.Rand, step int) randomOp {
	op := randomOp{
		day:       rng.IntN(35),
		candidate: fmt.Sprintf("c%d", rng.IntN(5)),
		position:  fmt.Sprintf("p%d", rng.IntN(3)),
		offer:     fmt.Sprintf("o%03d", maxInt(0, step-1-rng.IntN(maxInt(1, step)))),
		approval:  fmt.Sprintf("e%d", rng.IntN(3)),
		number:    70 + rng.IntN(70),
	}
	switch rng.IntN(11) {
	case 0:
		op.kind = "candidate"
	case 1:
		op.kind = "position"
		op.number = rng.IntN(4)
	case 2:
		op.kind = "freeze"
	case 3:
		op.kind = "open"
	case 4:
		op.kind = "adjust"
		op.number = rng.IntN(5)
	case 5:
		op.kind = "grant"
		op.number = rng.IntN(2)
	case 6:
		op.kind = "issue"
		op.offer = fmt.Sprintf("o%03d", step)
		op.deadline = op.day + rng.IntN(4)
	case 7:
		op.kind = "respond"
		op.accept = rng.IntN(2) == 0
		op.start = op.day + rng.IntN(4)
	case 8:
		op.kind = "withdraw"
	case 9:
		op.kind = "cancel"
	default:
		op.kind = []string{"onboard", "terminate"}[rng.IntN(2)]
	}
	return op
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func logRandomDecision(t *testing.T, step int, op randomOp, serviceErr, modelErr error) {
	t.Helper()
	t.Logf(
		"step=%d kind=%s day=%d candidate=%s position=%s offer=%s approval=%s number=%d deadline=%d start=%d accept=%t => service=%s model=%s",
		step, op.kind, op.day, op.candidate, op.position, op.offer, op.approval,
		op.number, op.deadline, op.start, op.accept,
		randomErrorCode(serviceErr), randomErrorCode(modelErr),
	)
	t.Logf("decision_basis=%s service_detail=%q model_detail=%q", decisionBasis(op, serviceErr), serviceErr, modelErr)
}

func decisionBasis(op randomOp, err error) string {
	if err != nil {
		return "rejected:" + string(randomErrorCode(err))
	}
	return "accepted:" + op.kind
}

func assertModelSnapshot(t *testing.T, s *Service, model *naiveModel) {
	t.Helper()
	got := s.Snapshot()
	want := Snapshot{
		LastAcceptedNow: model.clock,
		Positions:       model.positions,
		Offers:          model.offers,
		Exceptions:      model.exceptions,
		LastExit:        model.lastExit,
	}
	for _, position := range got.Positions {
		if position.Occupied() > position.Total {
			t.Fatalf("position %s over occupied: %d > %d", position.ID, position.Occupied(), position.Total)
		}
	}
	if got.LastAcceptedNow != want.LastAcceptedNow {
		t.Fatalf("clock got %d want %d", got.LastAcceptedNow, want.LastAcceptedNow)
	}
	if !reflect.DeepEqual(got.Positions, want.Positions) {
		t.Fatalf("positions differ\ngot: %#v\nwant: %#v", got.Positions, want.Positions)
	}
	if !reflect.DeepEqual(got.Offers, want.Offers) {
		t.Fatalf("offers differ\ngot: %#v\nwant: %#v", got.Offers, want.Offers)
	}
	if !reflect.DeepEqual(got.Exceptions, want.Exceptions) {
		t.Fatalf("exceptions differ\ngot: %#v\nwant: %#v", got.Exceptions, want.Exceptions)
	}
	if !reflect.DeepEqual(got.LastExit, want.LastExit) {
		t.Fatalf("last exits differ\ngot: %#v\nwant: %#v", got.LastExit, want.LastExit)
	}
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seed := uint64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1636+seed, 42+seed))
			s := NewService(4, 2)
			model := newNaiveModel(4, 2)
			for step := 0; step < 180; step++ {
				op := generateRandomOp(rng, step)
				if step < 8 {
					op = setupRandomOp(rng, step)
				}
				serviceErr := callService(s, op)
				modelErr := model.apply(op)
				logRandomDecision(t, step, op, serviceErr, modelErr)
				if randomErrorCode(serviceErr) != randomErrorCode(modelErr) {
					t.Fatalf("step %d error mismatch", step)
				}
				assertModelSnapshot(t, s, model)
			}
		})
	}
}

func setupRandomOp(rng *rand.Rand, step int) randomOp {
	day := 1 + step
	if step < 5 {
		return randomOp{kind: "candidate", day: day, candidate: fmt.Sprintf("c%d", step)}
	}
	position := step - 5
	return randomOp{
		kind:     "position",
		day:      day,
		position: fmt.Sprintf("p%d", position),
		number:   3,
	}
}
