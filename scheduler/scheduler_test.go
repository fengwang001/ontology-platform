package scheduler

import (
	"bytes"
	"log/slog"
	"testing"
)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

func newTestScheduler(t *testing.T, cfg Config, nodes []Node) *Scheduler {
	t.Helper()
	s, err := New(cfg, nodes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func threeZoneNodes() []Node {
	return []Node{
		{ID: "n-a1", Zone: "a", Slots: 2, Labels: map[string]string{"disk": "ssd"}},
		{ID: "n-b1", Zone: "b", Slots: 2, Labels: map[string]string{"disk": "ssd"}},
		{ID: "n-c1", Zone: "c", Slots: 2, Labels: map[string]string{"disk": "ssd"}},
	}
}

func mustSchedule(t *testing.T, s *Scheduler, groupID, replicaID string) Placement {
	t.Helper()
	p, err := s.Schedule(groupID, replicaID)
	if err != nil {
		t.Fatalf("schedule %s: %v (%s)", replicaID, err, ErrReason(err))
	}
	return p
}

func TestValidationReasons(t *testing.T) {
	if _, err := New(Config{}, []Node{{ID: "n1", Zone: "a", Slots: 0}}); ErrReason(err) != ReasonInvalidSlots {
		t.Fatalf("want invalid_slots, got %v", err)
	}
	if _, err := New(Config{}, []Node{{ID: "n1", Zone: "", Slots: 1}}); ErrReason(err) != ReasonEmptyZone {
		t.Fatalf("want empty_zone, got %v", err)
	}
	dup := []Node{{ID: "d", Zone: "a", Slots: 1}, {ID: "d", Zone: "b", Slots: 1}}
	if _, err := New(Config{}, dup); ErrReason(err) != ReasonDuplicateNode {
		t.Fatalf("want duplicate_node, got %v", err)
	}

	s := newTestScheduler(t, Config{}, threeZoneNodes())
	if err := s.AddGroup(Group{ID: "g", Skew: 0}); ErrReason(err) != ReasonInvalidSkew {
		t.Fatalf("want invalid_skew, got %v", err)
	}
	g := Group{ID: "g", Skew: 1, RequiredLabels: map[string]string{"disk": "hdd"}}
	if err := s.AddGroup(g); ErrReason(err) != ReasonNoMatchingNode {
		t.Fatalf("want no_matching_node, got %v", err)
	}
}

func TestRejectedOpsDoNotMutateState(t *testing.T) {
	s := newTestScheduler(t, Config{}, threeZoneNodes())
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(Group{ID: "bad", Skew: 0}); err == nil {
		t.Fatal("want skew error")
	}
	if _, err := s.ZoneCounts("bad"); ErrReason(err) != ReasonUnknownGroup {
		t.Fatalf("rejected group must not be registered, got %v", err)
	}

	p := mustSchedule(t, s, "g", "r1")
	usedBefore, _ := s.UsedSlots(p.NodeID)
	if _, err := s.Schedule("g", "r1"); ErrReason(err) != ReasonDuplicateReplica {
		t.Fatalf("want duplicate_replica, got %v", err)
	}
	if usedAfter, _ := s.UsedSlots(p.NodeID); usedAfter != usedBefore {
		t.Fatalf("used slots changed after rejection: %d -> %d", usedBefore, usedAfter)
	}
	if err := s.DeleteReplica("g", "nope"); ErrReason(err) != ReasonUnknownReplica {
		t.Fatalf("want unknown_replica, got %v", err)
	}
	if usedAfter, _ := s.UsedSlots(p.NodeID); usedAfter != usedBefore {
		t.Fatalf("used slots changed after failed delete: %d -> %d", usedBefore, usedAfter)
	}
}
