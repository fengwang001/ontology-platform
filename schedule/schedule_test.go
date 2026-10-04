package schedule_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/schedule"
)

func newExampleScheduler(t *testing.T) *schedule.Scheduler {
	t.Helper()
	s := schedule.NewScheduler()
	must(t, s.AddRoom("1", 30))
	must(t, s.AddRoom("2", 20))
	must(t, s.AddEquip("C", 1, 15))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, got, want error, reason string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: error = %v, want %v", reason, got, want)
	}
}

func equipmentType(err error) string {
	var conflict schedule.TypeError
	if errors.As(err, &conflict) {
		return conflict.Type
	}
	return ""
}

func TestBookBoundariesAndPriority(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *schedule.Scheduler)
	}{
		{
			name: "room end plus turn exactly equal is compatible",
			run: func(t *testing.T, s *schedule.Scheduler) {
				must(t, s.Book(0, "A", "1", 480, 120, "u", schedule.Needs{"C": 1}))
				assertError(t, s.Book(0, "B", "1", 629, 1, "x", nil), schedule.ErrRoomConflict, "629")
				must(t, s.Book(0, "C", "1", 630, 1, "x", nil))
			},
		},
		{
			name: "sterilization exactly released is compatible",
			run: func(t *testing.T, s *schedule.Scheduler) {
				must(t, s.Book(0, "A", "1", 480, 120, "u", schedule.Needs{"C": 1}))
				err := s.Book(0, "B", "2", 610, 90, "x", schedule.Needs{"C": 1})
				assertError(t, err, schedule.ErrEquipConflict, "610 equipment")
				if equipmentType(err) != "C" {
					t.Fatalf("conflict type = %q", equipmentType(err))
				}
				must(t, s.Book(0, "C", "2", 615, 1, "x", schedule.Needs{"C": 1}))
			},
		},
		{
			name: "surgeon intervals are allowed to touch",
			run: func(t *testing.T, s *schedule.Scheduler) {
				must(t, s.Book(0, "A", "1", 480, 120, "u", schedule.Needs{"C": 1}))
				must(t, s.Book(0, "B", "2", 600, 60, "u", nil))
				must(t, s.AddRoom("3", 0))
				assertError(t, s.Book(0, "C", "3", 650, 1, "u", nil), schedule.ErrSurgeonConflict, "overlap")
			},
		},
		{
			name: "invalid argument precedes clock and references",
			run: func(t *testing.T, s *schedule.Scheduler) {
				assertError(t, s.Book(10, "A", "missing", 5, 0, "u", nil), schedule.ErrInvalidArgument, "dur")
				assertError(t, s.Book(-1, "A", "1", 0, 1, "u", nil), schedule.ErrInvalidArgument, "now")
				assertError(t, s.Book(10, "A", "1", 11, 1, "u", schedule.Needs{"C": 9}), schedule.ErrInvalidArgument, "need over pool")
				must(t, s.Book(10, "A", "1", 11, 1, "u", nil))
				assertError(t, s.Book(9, "B", "missing", 10, 1, "u", nil), schedule.ErrClockRolledBack, "clock")
				assertError(t, s.Book(10, "A", "missing", 11, 1, "u", nil), schedule.ErrIDExists, "id")
				assertError(t, s.Book(10, "B", "missing", 11, 1, "u", nil), schedule.ErrUnknownRoom, "room")
				assertError(t, s.Book(10, "B", "1", 11, 1, "u", schedule.Needs{"X": 1}), schedule.ErrUnknownEquip, "equipment")
			},
		},
		{
			name: "cancel only removes unstarted elective surgery",
			run: func(t *testing.T, s *schedule.Scheduler) {
				must(t, s.Book(10, "A", "1", 20, 10, "u", nil))
				assertError(t, s.Cancel(20, "A"), schedule.ErrBadState, "started")
				assertError(t, s.Cancel(9, "A"), schedule.ErrClockRolledBack, "clock")
				must(t, s.Cancel(19, "A"))
				must(t, s.Book(19, "A", "1", 20, 10, "u", nil))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newExampleScheduler(t))
		})
	}
}

func TestExaminedIgnoresDistantSurgeries(t *testing.T) {
	makeScheduler := func(distant int) *schedule.Scheduler {
		s := newExampleScheduler(t)
		for i := 0; i < distant; i++ {
			start := int64(100000 + i*2000)
			must(t, s.Book(0, string(rune('a'+i))+"distant", "2", start, 10, "far", nil))
		}
		must(t, s.Book(0, "A", "1", 1000, 60, "u", nil))
		assertError(t, s.Book(0, "B", "1", 1010, 1, "u", nil), schedule.ErrRoomConflict, "conflict")
		return s
	}
	first := makeScheduler(100)
	second := makeScheduler(10000)
	if got, want := first.Examined(), second.Examined(); got != want {
		t.Fatalf("examined = %d and %d, want equal", got, want)
	}
}

func TestEmergencyRoomTieAndDisplacementOrder(t *testing.T) {
	s := schedule.NewScheduler()
	must(t, s.AddRoom("1", 0))
	must(t, s.AddRoom("2", 0))
	must(t, s.AddRoom("z", 1))
	must(t, s.AddEquip("C", 2, 0))
	must(t, s.Book(0, "R1", "1", 10, 10, "r1", nil))
	must(t, s.Book(0, "R2", "2", 10, 10, "r2", nil))
	must(t, s.Book(0, "RZ", "z", 10, 10, "rz", nil))
	result, err := s.Emergency(0, "E", 20, "em", nil)
	if err != nil {
		t.Fatalf("emergency: %v", err)
	}
	if result.Room != "1" || result.Start != 0 || len(result.Displaced) != 1 || result.Displaced[0].ID != "R1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertError(t, s.Cancel(1, "E"), schedule.ErrBadState, "emergency cannot cancel")
}

func TestEmergencyThreeStepOrder(t *testing.T) {
	s := schedule.NewScheduler()
	must(t, s.AddRoom("1", 10))
	must(t, s.AddRoom("2", 0))
	must(t, s.AddRoom("3", 0))
	must(t, s.AddRoom("4", 0))
	must(t, s.AddRoom("5", 0))
	must(t, s.AddEquip("C", 1, 0))
	blocked, err := s.Emergency(0, "BLOCK1", 30, "blocker1", nil)
	if err != nil {
		t.Fatalf("first blocking emergency: %v", err)
	}
	if blocked.Room != "1" || blocked.Start != 0 {
		t.Fatalf("blocking emergency landed in %+v", blocked)
	}
	blocked, err = s.Emergency(0, "BLOCK2", 30, "blocker2", nil)
	if err != nil {
		t.Fatalf("second blocking emergency: %v", err)
	}
	if blocked.Room != "2" || blocked.Start != 0 {
		t.Fatalf("second blocking emergency landed in %+v", blocked)
	}
	for _, roomID := range []string{"4", "5"} {
		must(t, s.Book(0, "block-"+roomID, roomID, 100, 10, "block-"+roomID, nil))
	}
	must(t, s.Book(0, "room-first", "3", 20, 10, "x", nil))
	must(t, s.Book(0, "surgeon", "4", 25, 10, "em-doc", nil))
	must(t, s.Book(0, "equip-new", "5", 20, 10, "e2", schedule.Needs{"C": 1}))
	result, err := s.Emergency(0, "E", 30, "em-doc", schedule.Needs{"C": 1})
	if err != nil {
		t.Fatalf("emergency: %v", err)
	}
	got := make([]string, len(result.Displaced))
	for i := range result.Displaced {
		got[i] = result.Displaced[i].ID
	}
	want := []string{"room-first", "surgeon", "equip-new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("displaced = %v, want %v", got, want)
	}
}

func TestEmergencyRejectionLeavesNoTrace(t *testing.T) {
	s := newExampleScheduler(t)
	must(t, s.Book(0, "A", "1", 480, 120, "u", schedule.Needs{"C": 1}))
	must(t, s.Book(0, "D", "2", 520, 60, "v", nil))
	_, err := s.Emergency(500, "E", 60, "u", nil)
	assertError(t, err, schedule.ErrSurgeonConflict, "surgeon before equipment")
	_, err = s.Emergency(500, "E", 60, "w", schedule.Needs{"C": 1})
	assertError(t, err, schedule.ErrEquipConflict, "non-displaceable equipment")
	must(t, s.Cancel(500, "D"))
	result, err := s.Emergency(500, "E", 60, "w", nil)
	if err != nil {
		t.Fatalf("successful emergency: %v", err)
	}
	if result.Room != "2" || result.Start != 500 || len(result.Displaced) != 0 {
		t.Fatalf("unexpected result after cancellation: %+v", result)
	}
}

func TestConcurrentOperations(t *testing.T) {
	s := newExampleScheduler(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			_ = s.Book(100, id, "1", 200+int64(i)*300, 20, "doctor", nil)
			_ = s.Cancel(101, id)
		}(i)
	}
	wg.Wait()
	if _, err := s.Emergency(102, "urgent", 10, "emergency-doc", nil); err != nil {
		t.Fatalf("emergency after concurrent work: %v", err)
	}
}

func TestEmergencyRejectionOrderAndEquipmentSort(t *testing.T) {
	s := newExampleScheduler(t)
	must(t, s.AddRoom("3", 0))
	must(t, s.AddRoom("4", 0))
	must(t, s.Book(0, "A", "1", 100, 20, "u", schedule.Needs{"C": 1}))
	must(t, s.Book(0, "new-late", "4", 15, 1, "l", schedule.Needs{"C": 1}))

	_, err := s.Emergency(0, "A", 0, "", nil)
	assertError(t, err, schedule.ErrInvalidArgument, "invalid before all")
	_, err = s.Emergency(-1, "E", 1, "", nil)
	assertError(t, err, schedule.ErrInvalidArgument, "invalid before clock")
	_, err = s.Emergency(-1, "A", 1, "w", nil)
	assertError(t, err, schedule.ErrInvalidArgument, "invalid before duplicate")
	_, err = s.Emergency(-1, "E", 1, "w", schedule.Needs{"X": 1})
	assertError(t, err, schedule.ErrInvalidArgument, "invalid before unknown equipment")
	_, err = s.Emergency(100, "A", 1, "u", schedule.Needs{"C": 1})
	assertError(t, err, schedule.ErrIDExists, "duplicate before surgeon")
	_, err = s.Emergency(100, "E", 1, "u", schedule.Needs{"C": 1})
	assertError(t, err, schedule.ErrSurgeonConflict, "surgeon before equipment")
	_, err = s.Emergency(100, "E", 1, "w", schedule.Needs{"X": 1})
	assertError(t, err, schedule.ErrUnknownEquip, "unknown type before equipment shortage")

	result, err := s.Emergency(0, "E", 25, "w", schedule.Needs{"C": 1})
	if err != nil {
		t.Fatalf("emergency: %v", err)
	}
	got := make([]string, len(result.Displaced))
	for i := range result.Displaced {
		got[i] = result.Displaced[i].ID
	}
	want := []string{"new-late"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("equipment displaced = %v, want %v", got, want)
	}
	must(t, s.Book(1, "new-late", "1", 500, 10, "reuse", nil))
}

func TestEmergencyEquipmentDisplacesLatestStartFirst(t *testing.T) {
	s := schedule.NewScheduler()
	must(t, s.AddRoom("1", 0))
	must(t, s.AddRoom("2", 0))
	must(t, s.AddRoom("3", 0))
	must(t, s.AddRoom("4", 0))
	must(t, s.AddRoom("5", 0))
	must(t, s.AddEquip("C", 2, 0))
	must(t, s.AddEquip("Z", 1, 0))
	blocked, err := s.Emergency(0, "BLOCK", 20, "blocker", nil)
	if err != nil {
		t.Fatalf("block emergency: %v", err)
	}
	if blocked.Room != "1" || blocked.Start != 0 {
		t.Fatalf("block emergency = %+v", blocked)
	}
	must(t, s.Book(0, "early", "2", 5, 10, "e", schedule.Needs{"C": 1}))
	must(t, s.Book(0, "late", "3", 20, 10, "l", schedule.Needs{"C": 1}))
	must(t, s.Book(0, "keeper", "4", 5, 10, "k", schedule.Needs{"Z": 1}))
	result, err := s.Emergency(0, "E", 30, "em", schedule.Needs{"C": 2})
	if err != nil {
		t.Fatalf("emergency: %v", err)
	}
	if len(result.Displaced) != 2 || result.Displaced[0].ID != "late" || result.Displaced[1].ID != "early" {
		t.Fatalf("room=%s start=%d displaced = %+v, want only late in room 5", result.Room, result.Start, result.Displaced)
	}
}
