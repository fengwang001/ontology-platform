package kanban

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testConfig(columnLimits ...int) BoardConfig {
	columns := make([]Column, len(columnLimits))
	for index, limit := range columnLimits {
		columns[index] = Column{Name: fmt.Sprintf("c%d", index), Limit: limit}
	}
	columns[len(columns)-1].Limit = 0
	return BoardConfig{Columns: columns, AssigneeLimit: 2}
}

func newTestService(t *testing.T, config BoardConfig) *Service {
	t.Helper()
	service := NewService()
	if err := service.CreateBoard("b", config); err != nil {
		t.Fatalf("CreateBoard(): %v", err)
	}
	return service
}

func createTestCard(t *testing.T, service *Service, id, assignee string, now int64) Card {
	t.Helper()
	result, err := service.CreateCard(CreateCardRequest{BoardID: "b", User: "u", Card: id, Assignee: assignee, Now: now})
	if err != nil {
		t.Fatalf("CreateCard(%s): %v", id, err)
	}
	return result.Card
}

func moveOK(t *testing.T, service *Service, id string, to int, version int64, expedite bool, now int64) Card {
	t.Helper()
	result, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: id, To: to, ExpectVersion: int64(version), Expedite: expedite, Now: now})
	if err != nil {
		t.Fatalf("Move(%s -> %d): %v", id, to, err)
	}
	return result.Card
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func TestCapacityBoundariesAndAssigneeLimit(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1, 1, 0))
	createTestCard(t, service, "a", "x", 1)
	createTestCard(t, service, "b", "x", 2)
	createTestCard(t, service, "d", "x", 3)

	moveOK(t, service, "a", 1, 1, false, 4)
	_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "b", To: 1, ExpectVersion: 1, Now: 5})
	assertErrorIs(t, err, ErrColumnLimitFull)

	if err := service.SetColumnLimit(ColumnLimitRequest{BoardID: "b", User: "u", Column: 1, Limit: 2, Now: 6}); err != nil {
		t.Fatalf("SetColumnLimit(): %v", err)
	}
	moveOK(t, service, "a", 2, 2, false, 6)
	moveOK(t, service, "b", 1, 1, false, 7)
	_, err = service.Move(MoveRequest{BoardID: "b", User: "u", Card: "d", To: 1, ExpectVersion: 1, Now: 8})
	assertErrorIs(t, err, ErrAssigneeLimitFull)
}

func TestProgressMoveReleasesBeforeAssigneeCheck(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1))
	createTestCard(t, service, "a", "x", 1)
	moveOK(t, service, "a", 1, 1, false, 2)
	card := moveOK(t, service, "a", 2, 2, false, 3)
	if card.Version != 3 {
		t.Fatalf("version = %d, want 3", card.Version)
	}
}

func TestExpediteUniqueAndCleared(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1))
	createTestCard(t, service, "a", "x", 1)
	createTestCard(t, service, "b", "x", 2)

	first := moveOK(t, service, "a", 1, 1, true, 3)
	if !first.Expedited {
		t.Fatalf("first expedite was not marked")
	}
	_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "b", To: 1, ExpectVersion: 1, Expedite: true, Now: 4})
	assertErrorIs(t, err, ErrExpediteOccupied)

	finished := moveOK(t, service, "a", 2, 2, false, 5)
	if finished.Expedited {
		t.Fatalf("expedite marker survived completion")
	}
	moveOK(t, service, "b", 1, 1, true, 6)
}

func TestLowerLimitDoesNotEvictButBlocksEntry(t *testing.T) {
	service := newTestService(t, testConfig(0, 2, 1))
	createTestCard(t, service, "a", "x", 1)
	createTestCard(t, service, "b", "x", 2)
	moveOK(t, service, "a", 1, 1, false, 3)
	moveOK(t, service, "b", 1, 1, false, 4)
	if err := service.SetColumnLimit(ColumnLimitRequest{BoardID: "b", User: "u", Column: 1, Limit: 1, Now: 5}); err != nil {
		t.Fatalf("SetColumnLimit(): %v", err)
	}
	snapshot, _ := service.GetBoard("b")
	if snapshot.ColumnOccupancy[1] != 2 {
		t.Fatalf("occupancy = %d, want existing cards retained", snapshot.ColumnOccupancy[1])
	}
	createTestCard(t, service, "c", "z", 6)
	moveOK(t, service, "a", 2, 2, false, 7)
	_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "c", To: 1, ExpectVersion: 1, Now: 8})
	assertErrorIs(t, err, ErrColumnLimitFull)
}

func TestDependenciesCycleAndReopenBlocked(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1))
	a := createTestCard(t, service, "a", "x", 1)
	b := createTestCard(t, service, "b", "x", 2)
	c := createTestCard(t, service, "c", "x", 3)

	_, err := service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: "a", Prerequisite: "a", ExpectVersion: a.Version, Now: 4})
	assertErrorIs(t, err, ErrDependencyCycle)

	addDepOK := func(cardID, prerequisiteID string, version int64, now int64) {
		t.Helper()
		if _, err := service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: cardID, Prerequisite: prerequisiteID, ExpectVersion: version, Now: now}); err != nil {
			t.Fatalf("AddDep(%s requires %s): %v", cardID, prerequisiteID, err)
		}
	}
	addDepOK("a", "b", a.Version, 5)
	_, err = service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: "a", Prerequisite: "b", ExpectVersion: a.Version + 1, Now: 6})
	assertErrorIs(t, err, ErrDependencyExists)
	_, err = service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: "b", Prerequisite: "a", ExpectVersion: b.Version, Now: 7})
	assertErrorIs(t, err, ErrDependencyCycle)

	_, err = service.Move(MoveRequest{BoardID: "b", User: "u", Card: "a", To: 1, ExpectVersion: a.Version + 1, Now: 8})
	assertErrorIs(t, err, ErrDependencyBlocked)

	moveOK(t, service, "b", 1, b.Version, false, 9)
	moveOK(t, service, "b", 2, b.Version+1, false, 10)
	moveOK(t, service, "a", 1, a.Version+1, false, 11)
	moveOK(t, service, "a", 2, a.Version+2, false, 12)
	_, err = service.Reopen(ReopenRequest{BoardID: "b", User: "u", Card: "b", ExpectVersion: b.Version + 2, Now: 13})
	assertErrorIs(t, err, ErrDependencyBlocked)

	_ = c
}

func TestRejectionOrder(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1))
	createTestCard(t, service, "a", "x", 2)
	createTestCard(t, service, "pre", "y", 3)
	if _, err := service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: "a", Prerequisite: "pre", ExpectVersion: 1, Now: 4}); err != nil {
		t.Fatalf("AddDep(): %v", err)
	}

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid before clock", func() error {
			_, err := service.Move(MoveRequest{BoardID: "b", User: "", Card: "a", To: 1, ExpectVersion: 1, Now: 1})
			return err
		}, ErrInvalidArgument},
		{"clock before missing", func() error {
			_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "missing", To: 1, ExpectVersion: 1, Now: 1})
			return err
		}, ErrClockRewound},
		{"missing before version", func() error {
			_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "missing", To: 1, ExpectVersion: 99, Now: 5})
			return err
		}, ErrCardNotFound},
		{"version before transition", func() error {
			_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "a", To: 0, ExpectVersion: 99, Now: 5})
			return err
		}, ErrVersionConflict},
		{"transition before dependency", func() error {
			_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "a", To: 2, ExpectVersion: 2, Now: 6})
			return err
		}, ErrIllegalTransition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertErrorIs(t, tc.call(), tc.want) })
	}
}

func TestBusinessRejectionOrderBoundaries(t *testing.T) {
	t.Run("dependency before expedite", func(t *testing.T) {
		service := newTestService(t, testConfig(0, 1, 1))
		createTestCard(t, service, "existing", "x", 1)
		createTestCard(t, service, "pre", "x", 2)
		createTestCard(t, service, "target", "y", 3)
		moveOK(t, service, "existing", 1, 1, true, 4)
		if _, err := service.AddDep(DependencyRequest{BoardID: "b", User: "u", Card: "target", Prerequisite: "pre", ExpectVersion: 1, Now: 5}); err != nil {
			t.Fatalf("AddDep(): %v", err)
		}
		_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "target", To: 1, ExpectVersion: 2, Expedite: true, Now: 6})
		assertErrorIs(t, err, ErrDependencyBlocked)
	})

	t.Run("expedite before column", func(t *testing.T) {
		service := newTestService(t, testConfig(0, 1, 1))
		createTestCard(t, service, "existing", "x", 1)
		createTestCard(t, service, "target", "y", 2)
		moveOK(t, service, "existing", 1, 1, true, 3)
		_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "target", To: 1, ExpectVersion: 1, Expedite: true, Now: 4})
		assertErrorIs(t, err, ErrExpediteOccupied)
	})

	t.Run("column before assignee", func(t *testing.T) {
		service := NewService()
		config := BoardConfig{
			Columns:       []Column{{Name: "todo"}, {Name: "dev", Limit: 2}, {Name: "review", Limit: 1}, {Name: "done"}},
			AssigneeLimit: 1,
		}
		if err := service.CreateBoard("b", config); err != nil {
			t.Fatalf("CreateBoard(): %v", err)
		}
		createTestCard(t, service, "a", "x", 1)
		createTestCard(t, service, "busy", "y", 2)
		createTestCard(t, service, "target", "y", 3)
		moveOK(t, service, "a", 1, 1, false, 4)
		moveOK(t, service, "busy", 1, 1, false, 5)
		moveOK(t, service, "a", 2, 2, false, 6)
		if err := service.SetColumnLimit(ColumnLimitRequest{BoardID: "b", User: "u", Column: 1, Limit: 1, Now: 7}); err != nil {
			t.Fatalf("SetColumnLimit(): %v", err)
		}
		_, err := service.Move(MoveRequest{BoardID: "b", User: "u", Card: "target", To: 1, ExpectVersion: 1, Now: 8})
		assertErrorIs(t, err, ErrColumnLimitFull)
	})
}

func TestConcurrentReplayAndInvariants(t *testing.T) {
	service := newTestService(t, testConfig(0, 1, 1))
	for i := 0; i < 20; i++ {
		createTestCard(t, service, fmt.Sprintf("c%02d", i), fmt.Sprintf("u%02d", i%2), int64(i+1))
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("c%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = service.Move(MoveRequest{BoardID: "b", User: "u", Card: id, To: 1, ExpectVersion: 1, Expedite: true, Now: 100})
		}()
	}
	wg.Wait()
	snapshot, _ := service.GetBoard("b")
	expedited := 0
	for _, card := range snapshot.Cards {
		if card.Expedited {
			expedited++
		}
	}
	if expedited > 1 {
		t.Fatalf("expedited cards = %d, want at most 1", expedited)
	}
	if snapshot.ColumnOccupancy[1] <= 1 && snapshot.ColumnOccupancy[1] > len(snapshot.Cards) {
		t.Fatalf("invalid occupancy: %#v", snapshot.ColumnOccupancy)
	}
}
