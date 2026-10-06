package staffing

import (
	"errors"
	"sync"
	"testing"
)

func newTestService(t *testing.T, cooldown, grace int) *Service {
	t.Helper()
	return NewService(cooldown, grace)
}

func mustAddCandidate(t *testing.T, s *Service, day int, id string) {
	t.Helper()
	if err := s.AddCandidate(day, id); err != nil {
		t.Fatalf("AddCandidate(%q): %v", id, err)
	}
}

func mustAddPosition(t *testing.T, s *Service, day int, p Position) {
	t.Helper()
	if err := s.AddPosition(day, p); err != nil {
		t.Fatalf("AddPosition(%q): %v", p.ID, err)
	}
}

func mustGrant(t *testing.T, s *Service, day int, positionID, approvalID string, uses int) {
	t.Helper()
	if err := s.GrantException(day, positionID, approvalID, uses); err != nil {
		t.Fatalf("GrantException(%q): %v", approvalID, err)
	}
}

func mustIssue(t *testing.T, s *Service, in IssueOfferInput) {
	t.Helper()
	if _, err := s.IssueOffer(in); err != nil {
		t.Fatalf("IssueOffer(%q): %v", in.OfferID, err)
	}
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var serviceErr *Error
	if !errors.As(err, &serviceErr) {
		t.Fatalf("error %v is not staffing.Error", err)
	}
	if serviceErr.Code != want {
		t.Fatalf("error code = %s, want %s (index %d): %v", serviceErr.Code, want, serviceErr.Index, err)
	}
}

func assertOccupied(t *testing.T, s *Service, positionID string, onDuty, pending int) {
	t.Helper()
	got := s.Snapshot().Positions[positionID]
	if got.OnDuty != onDuty || got.Pending != pending {
		t.Fatalf("%s occupancy = onDuty %d pending %d, want %d/%d", positionID, got.OnDuty, got.Pending, onDuty, pending)
	}
}

func TestDeterministicBoundaries(t *testing.T) {
	t.Run("occupied exactly full rejects another issue", func(t *testing.T) {
		s := newTestService(t, 3, 2)
		mustAddCandidate(t, s, 0, "c1")
		mustAddCandidate(t, s, 0, "c2")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c1", PositionID: "p", Salary: 110, Deadline: 5})
		_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "o2", CandidateID: "c2", PositionID: "p", Salary: 110, Deadline: 6})
		assertErrorCode(t, err, ErrHeadcountFull)
		assertOccupied(t, s, "p", 0, 1)
	})

	t.Run("salary band endpoints are inclusive", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "low")
		mustAddCandidate(t, s, 0, "high")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 2})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "lo", CandidateID: "low", PositionID: "p", Salary: 100, Deadline: 5})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "hi", CandidateID: "high", PositionID: "p", Salary: 120, Deadline: 5})
		assertOccupied(t, s, "p", 0, 2)
	})

	t.Run("exception quarter boundary", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 89, "c1")
		mustAddCandidate(t, s, 89, "c2")
		mustAddPosition(t, s, 89, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 2})
		mustGrant(t, s, 89, "p", "ex", 1)
		mustIssue(t, s, IssueOfferInput{Now: 89, OfferID: "o1", CandidateID: "c1", PositionID: "p", Salary: 130, Deadline: 100, ExceptionApprovalID: "ex"})
		mustIssue(t, s, IssueOfferInput{Now: 90, OfferID: "o2", CandidateID: "c2", PositionID: "p", Salary: 130, Deadline: 100, ExceptionApprovalID: "ex"})
		used := s.Snapshot().Exceptions["ex"].Used
		if used[0] != 1 || used[1] != 1 {
			t.Fatalf("used by quarter = %v, want q0=1 q1=1", used)
		}
	})

	t.Run("cooldown exactly elapsed is allowed", func(t *testing.T) {
		s := newTestService(t, 5, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 2})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 10})
		if err := s.RespondToOffer(3, "o1", false, 0); err != nil {
			t.Fatal(err)
		}
		mustIssue(t, s, IssueOfferInput{Now: 8, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 10})
	})

	t.Run("response deadline exact and one day late", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.RespondToOffer(5, "o", true, 6); err != nil {
			t.Fatal(err)
		}

		s = newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		err := s.RespondToOffer(6, "o", true, 7)
		assertErrorCode(t, err, ErrOfferExpired)
		assertOccupied(t, s, "p", 0, 0)
	})

	t.Run("lazy expiration releases when touched", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		assertOccupied(t, s, "p", 0, 1)
		err := s.WithdrawOffer(6, "o")
		assertErrorCode(t, err, ErrOfferExpired)
		assertOccupied(t, s, "p", 0, 0)
	})

	t.Run("accepted grace exactly elapsed can onboard", func(t *testing.T) {
		s := newTestService(t, 0, 2)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.RespondToOffer(5, "o", true, 6); err != nil {
			t.Fatal(err)
		}
		if err := s.Onboard(8, "o"); err != nil {
			t.Fatal(err)
		}
		assertOccupied(t, s, "p", 1, 0)
	})

	t.Run("frozen position accepts response", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.SetPositionStatus(1, "p", PositionFrozen); err != nil {
			t.Fatal(err)
		}
		if err := s.RespondToOffer(2, "o", true, 3); err != nil {
			t.Fatal(err)
		}
		if err := s.Onboard(3, "o"); err != nil {
			t.Fatal(err)
		}
		assertOccupied(t, s, "p", 1, 0)
	})

	t.Run("adjust down exactly to occupied", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 3})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.AdjustHeadcount(1, "p", 1); err != nil {
			t.Fatal(err)
		}
		if got := s.Snapshot().Positions["p"].Total; got != 1 {
			t.Fatalf("total = %d, want 1", got)
		}
	})

	t.Run("cancel releases without cooldown", func(t *testing.T) {
		s := newTestService(t, 10, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.RespondToOffer(1, "o1", true, 2); err != nil {
			t.Fatal(err)
		}
		if err := s.CancelAcceptedOffer(2, "o1"); err != nil {
			t.Fatal(err)
		}
		mustIssue(t, s, IssueOfferInput{Now: 2, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		assertOccupied(t, s, "p", 0, 1)
	})

	t.Run("abandoned offer starts cooldown", func(t *testing.T) {
		s := newTestService(t, 5, 1)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 1})
		if err := s.RespondToOffer(0, "o1", true, 1); err != nil {
			t.Fatal(err)
		}
		err := s.Onboard(3, "o1")
		assertErrorCode(t, err, ErrOfferExpired)
		_, err = s.IssueOffer(IssueOfferInput{Now: 3, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		assertErrorCode(t, err, ErrCoolingDown)
	})

	t.Run("termination releases one on-duty seat", func(t *testing.T) {
		s := newTestService(t, 0, 0)
		mustAddCandidate(t, s, 0, "c")
		mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 1})
		mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5})
		if err := s.RespondToOffer(0, "o1", true, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.Onboard(0, "o1"); err != nil {
			t.Fatal(err)
		}
		if err := s.TerminateEmployment(1, "o1"); err != nil {
			t.Fatal(err)
		}
		assertOccupied(t, s, "p", 0, 0)
		assertErrorCode(t, s.TerminateEmployment(2, "o1"), ErrStatusNotAllowed)
		assertOccupied(t, s, "p", 0, 0)
	})
}

func TestBatchIsAtomic(t *testing.T) {
	s := newTestService(t, 0, 0)
	mustAddCandidate(t, s, 0, "c1")
	mustAddCandidate(t, s, 0, "c2")
	mustAddCandidate(t, s, 0, "c3")
	mustAddPosition(t, s, 0, Position{ID: "p", Level: "L4", MinSalary: 100, MaxSalary: 120, Total: 2})

	err := s.BatchIssueOffers([]IssueOfferInput{
		{Now: 1, OfferID: "ok1", CandidateID: "c1", PositionID: "p", Salary: 110, Deadline: 5},
		{Now: 1, OfferID: "bad2", CandidateID: "c2", PositionID: "missing", Salary: 110, Deadline: 5},
		{Now: 1, OfferID: "ok3", CandidateID: "c3", PositionID: "p", Salary: 110, Deadline: 5},
	})
	assertErrorCode(t, err, ErrNotFound)
	var serviceErr *Error
	errors.As(err, &serviceErr)
	if serviceErr.Index != 1 {
		t.Fatalf("failure index = %d, want 1", serviceErr.Index)
	}
	snapshot := s.Snapshot()
	if len(snapshot.Offers) != 0 {
		t.Fatalf("offers after failed batch = %d, want 0", len(snapshot.Offers))
	}
	if snapshot.Positions["p"].Occupied() != 0 {
		t.Fatalf("occupied after failed batch = %d, want 0", snapshot.Positions["p"].Occupied())
	}
	if snapshot.LastAcceptedNow != 0 {
		t.Fatalf("clock after failed batch = %d, want 0", snapshot.LastAcceptedNow)
	}
}

func TestBatchRejectsDuplicateCandidateAtomic(t *testing.T) {
	s := newTestService(t, 0, 0)
	mustAddCandidate(t, s, 0, "c")
	mustAddPosition(t, s, 0, Position{ID: "p", Level: "L", MinSalary: 100, MaxSalary: 120, Total: 2})

	err := s.BatchIssueOffers([]IssueOfferInput{
		{Now: 1, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5},
		{Now: 1, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 110, Deadline: 5},
	})
	assertErrorCode(t, err, ErrCandidateHasPendingOffer)
	var serviceErr *Error
	errors.As(err, &serviceErr)
	if serviceErr.Index != 1 {
		t.Fatalf("index = %d, want 1", serviceErr.Index)
	}
	if len(s.Snapshot().Offers) != 0 || s.Snapshot().Positions["p"].Occupied() != 0 {
		t.Fatalf("duplicate batch left state: offers=%d occupied=%d", len(s.Snapshot().Offers), s.Snapshot().Positions["p"].Occupied())
	}
}

func TestRejectionPriority(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Service) error
		want ErrorCode
	}{
		{"invalid before clock", func(s *Service) error {
			return s.AddPosition(1, Position{ID: "p", Total: 1})
		}, ErrInvalidArgument},
		{"clock before missing", func(s *Service) error {
			if err := s.AddCandidate(2, "c"); err != nil {
				t.Fatal(err)
			}
			return s.AdjustHeadcount(1, "missing", 1)
		}, ErrClockRolledBack},
		{"missing before frozen", func(s *Service) error {
			_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "o", CandidateID: "missing", PositionID: "missing", Salary: 1, Deadline: 2})
			return err
		}, ErrNotFound},
		{"frozen before full", func(s *Service) error {
			mustAddCandidate(t, s, 1, "c")
			mustAddPosition(t, s, 1, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 2, Total: 0, Status: PositionFrozen})
			_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 2, Deadline: 2})
			return err
		}, ErrPositionFrozen},
		{"full before salary", func(s *Service) error {
			mustAddCandidate(t, s, 1, "c")
			mustAddPosition(t, s, 1, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 2, Total: 0})
			_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "o", CandidateID: "c", PositionID: "p", Salary: 99, Deadline: 2})
			return err
		}, ErrHeadcountFull},
		{"salary before existing offer", func(s *Service) error {
			mustAddCandidate(t, s, 1, "c1")
			mustAddCandidate(t, s, 1, "c2")
			mustAddPosition(t, s, 1, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 2, Total: 2})
			mustIssue(t, s, IssueOfferInput{Now: 1, OfferID: "old", CandidateID: "c1", PositionID: "p", Salary: 1, Deadline: 5})
			_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "new", CandidateID: "c1", PositionID: "p", Salary: 99, Deadline: 5})
			return err
		}, ErrSalaryBandWithoutApproval},
		{"existing offer before cooldown", func(s *Service) error {
			mustAddCandidate(t, s, 0, "c")
			mustAddPosition(t, s, 0, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 2, Total: 2})
			mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 1, Deadline: 5})
			if err := s.RespondToOffer(0, "o1", false, 0); err != nil {
				t.Fatal(err)
			}
			mustIssue(t, s, IssueOfferInput{Now: 10, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 1, Deadline: 15})
			_, err := s.IssueOffer(IssueOfferInput{Now: 10, OfferID: "o3", CandidateID: "c", PositionID: "p", Salary: 1, Deadline: 15})
			return err
		}, ErrCandidateHasPendingOffer},
		{"cooldown before expiry", func(s *Service) error {
			mustAddCandidate(t, s, 0, "c")
			mustAddPosition(t, s, 0, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 2, Total: 2})
			mustIssue(t, s, IssueOfferInput{Now: 0, OfferID: "o1", CandidateID: "c", PositionID: "p", Salary: 1, Deadline: 5})
			if err := s.RespondToOffer(0, "o1", false, 0); err != nil {
				t.Fatal(err)
			}
			_, err := s.IssueOffer(IssueOfferInput{Now: 0, OfferID: "o2", CandidateID: "c", PositionID: "p", Salary: 1, Deadline: 0})
			return err
		}, ErrCoolingDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewService(10, 10)
			err := tt.run(s)
			assertErrorCode(t, err, tt.want)
		})
	}
}

func TestConcurrentIssueIsSerializableAndNeverOverOccupies(t *testing.T) {
	s := newTestService(t, 0, 0)
	mustAddPosition(t, s, 0, Position{ID: "p", Level: "L", MinSalary: 1, MaxSalary: 1, Total: 10})
	for i := 0; i < 100; i++ {
		candidateID := "c" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		_ = s.AddCandidate(0, candidateID)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidateID := "c" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			_, err := s.IssueOffer(IssueOfferInput{Now: 1, OfferID: "o" + candidateID, CandidateID: candidateID, PositionID: "p", Salary: 1, Deadline: 5})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes, failures := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
			assertErrorCode(t, err, ErrHeadcountFull)
		}
	}
	position := s.Snapshot().Positions["p"]
	if successes != 10 || failures != 90 || position.Occupied() != 10 {
		t.Fatalf("successes=%d failures=%d occupied=%d", successes, failures, position.Occupied())
	}
}
