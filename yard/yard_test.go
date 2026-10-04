package yard_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/yard"
)

type assignmentPair struct {
	truck string
	dock  string
}

func TestBoundaryAndBookingRules(t *testing.T) {
	t.Run("start validation", func(t *testing.T) {
		cases := []struct {
			name      string
			start     int64
			now       int64
			wantError error
		}{
			{"valid", 100, 100, nil},
			{"before now", 80, 100, yard.ErrInvalidArgument},
			{"not multiple", 101, 100, yard.ErrInvalidArgument},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, _ := yard.NewScheduler(20, 30, 15, 60, 2)
				_, err := s.Book([]byte("T"), yard.Dry, tc.start, tc.now)
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("err=%v want=%v", err, tc.wantError)
				}
			})
		}
	})

	t.Run("capacity equality", func(t *testing.T) {
		s, _ := yard.NewScheduler(20, 30, 15, 200, 2)
		mustBook(t, s, "A", yard.Reefer, 100, 0)
		mustBook(t, s, "B", yard.Reefer, 100, 0)
		_, err := s.Book([]byte("C"), yard.Reefer, 100, 0)
		if !errors.Is(err, yard.ErrCapacity) {
			t.Fatalf("third booking: %v", err)
		}
	})

	t.Run("check-in boundaries", func(t *testing.T) {
		for _, tc := range []struct {
			now    int64
			onTime bool
		}{{69, false}, {70, true}, {115, true}, {116, false}} {
			s, _ := yard.NewScheduler(20, 30, 15, 60, 2)
			mustBook(t, s, "T", yard.Dry, 100, 0)
			list, err := s.CheckIn([]byte("T"), yard.Dry, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if tc.onTime {
				list = mustAddDock(t, s, "D", yard.Dry, tc.now)
			}
			if got := len(list) == 1; got != tc.onTime {
				t.Fatalf("now=%d assigned=%v want=%v", tc.now, got, tc.onTime)
			}
		}
	})
}

func TestAssignmentExamples(t *testing.T) {
	t.Run("given sequence", func(t *testing.T) {
		s := exampleScheduler(t)
		assertPairs(t, mustDepart(t, s, "P2", 120), []assignmentPair{{"T2", "R1"}})
		assertPairs(t, mustDepart(t, s, "P1", 145), []assignmentPair{{"W1", "D1"}})
		assertPairs(t, mustDepart(t, s, "P3", 150), []assignmentPair{{"T3", "R2"}})
	})

	t.Run("promotion exact versus one minute early", func(t *testing.T) {
		assertPairs(t, mustDepart(t, exampleScheduler(t), "P1", 144), []assignmentPair{{"T3", "D1"}})
		assertPairs(t, mustDepart(t, exampleScheduler(t), "P1", 145), []assignmentPair{{"W1", "D1"}})
	})

	t.Run("later check-in with earlier appointment starts first", func(t *testing.T) {
		s, _ := yard.NewScheduler(20, 30, 15, 60, 2)
		mustBook(t, s, "later", yard.Dry, 120, 10)
		mustBook(t, s, "earlier", yard.Dry, 100, 10)
		mustAddDock(t, s, "AD", yard.Dry, 10)
		mustAddDock(t, s, "AR", yard.Reefer, 10)
		mustCheckIn(t, s, "AD", yard.Dry, 20)
		mustCheckIn(t, s, "AR", yard.Reefer, 20)
		mustCheckIn(t, s, "later", yard.Dry, 105)
		mustCheckIn(t, s, "earlier", yard.Dry, 115)
		assertPairs(t, mustDepart(t, s, "AD", 115),
			[]assignmentPair{{"earlier", "AD"}})
	})
}

func TestRejectionOrder(t *testing.T) {
	s, _ := yard.NewScheduler(20, 30, 15, 60, 2)
	if _, err := s.Book(nil, yard.Dry, 100, 10); !errors.Is(err, yard.ErrInvalidArgument) {
		t.Fatalf("invalid: %v", err)
	}
	mustBook(t, s, "T", yard.Dry, 100, 10)
	if _, err := s.AddDock([]byte("D1"), yard.Dry, 9); !errors.Is(err, yard.ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
	mustAddDock(t, s, "D1", yard.Dry, 10)
	if _, err := s.AddDock([]byte("D1"), yard.Reefer, 10); !errors.Is(err, yard.ErrConflict) {
		t.Fatalf("duplicate dock: %v", err)
	}
	if _, err := s.Book([]byte("T"), yard.Reefer, 120, 10); !errors.Is(err, yard.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	mustCheckIn(t, s, "H", yard.Dry, 100)
	if _, err := s.CheckIn([]byte("T"), yard.Reefer, 130); !errors.Is(err, yard.ErrState) {
		t.Fatalf("kind mismatch: %v", err)
	}
	if _, err := s.Depart([]byte("ghost"), 130); !errors.Is(err, yard.ErrNotFound) {
		t.Fatalf("unknown depart after advance: %v", err)
	}
	mustCheckIn(t, s, "T", yard.Dry, 130)
	if _, err := s.CheckIn([]byte("T"), yard.Dry, 130); !errors.Is(err, yard.ErrState) {
		t.Fatalf("repeat check-in: %v", err)
	}
	if _, err := s.Depart([]byte("T"), 130); !errors.Is(err, yard.ErrState) {
		t.Fatalf("depart waiting: %v", err)
	}
}

func TestLookedBoundIndependentOfWaiters(t *testing.T) {
	counts := make([]int, 2)
	for i, waiters := range []int{100, 10000} {
		s, _ := yard.NewScheduler(20, 30, 15, 1_000_000, 1000)
		for id := 0; id < waiters; id++ {
			mustCheckIn(t, s, fmt.Sprintf("W%05d", id), yard.Dry, 1)
		}
		mustAddDock(t, s, "D1", yard.Dry, 2)
		counts[i] = s.LookedCountForTest()
		if counts[i] > 12 {
			t.Fatalf("waiters=%d looked=%d > 12", waiters, counts[i])
		}
	}
	if counts[0] != counts[1] {
		t.Fatalf("counts should be independent of waiter count: %v", counts)
	}
}

func exampleScheduler(t *testing.T) *yard.Scheduler {
	t.Helper()
	s, _ := yard.NewScheduler(20, 30, 15, 60, 2)
	mustAddDock(t, s, "D1", yard.Dry, 0)
	mustAddDock(t, s, "R1", yard.Reefer, 0)
	mustAddDock(t, s, "R2", yard.Reefer, 0)
	mustCheckIn(t, s, "P1", yard.Dry, 1)
	mustCheckIn(t, s, "P2", yard.Reefer, 2)
	mustCheckIn(t, s, "P3", yard.Reefer, 3)
	mustBook(t, s, "T1", yard.Dry, 100, 5)
	mustBook(t, s, "T2", yard.Reefer, 120, 6)
	mustBook(t, s, "T3", yard.Dry, 100, 7)
	mustCheckIn(t, s, "T3", yard.Dry, 80)
	mustCheckIn(t, s, "W1", yard.Dry, 85)
	mustCheckIn(t, s, "T1", yard.Dry, 116)
	mustCheckIn(t, s, "T2", yard.Reefer, 118)
	return s
}

func mustBook(t *testing.T, s *yard.Scheduler, truck string, kind yard.Kind, start, now int64) {
	t.Helper()
	if _, err := s.Book([]byte(truck), kind, start, now); err != nil {
		t.Fatalf("book %s: %v", truck, err)
	}
}

func mustAddDock(t *testing.T, s *yard.Scheduler, dockID string, kind yard.Kind, now int64) []yard.Assignment {
	t.Helper()
	list, err := s.AddDock([]byte(dockID), kind, now)
	if err != nil {
		t.Fatalf("add dock %s: %v", dockID, err)
	}
	return list
}

func mustCheckIn(t *testing.T, s *yard.Scheduler, truck string, kind yard.Kind, now int64) []yard.Assignment {
	t.Helper()
	list, err := s.CheckIn([]byte(truck), kind, now)
	if err != nil {
		t.Fatalf("check in %s: %v", truck, err)
	}
	return list
}

func mustDepart(t *testing.T, s *yard.Scheduler, truck string, now int64) []yard.Assignment {
	t.Helper()
	list, err := s.Depart([]byte(truck), now)
	if err != nil {
		t.Fatalf("depart %s: %v", truck, err)
	}
	return list
}

func assertPairs(t *testing.T, got []yard.Assignment, want []assignmentPair) {
	t.Helper()
	converted := make([]assignmentPair, 0, len(got))
	for _, item := range got {
		converted = append(converted, assignmentPair{string(item.Truck), string(item.Dock)})
	}
	if !reflect.DeepEqual(converted, want) {
		t.Fatalf("assignments=%v want=%v", converted, want)
	}
}
