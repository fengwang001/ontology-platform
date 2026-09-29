package scheduler

import (
	"errors"
	"sync"
	"testing"
)

func TestBindFailureReleasesReservation(t *testing.T) {
	injected := errors.New("injected bind failure")
	var calls []string
	binder := func(groupID, replicaID, nodeID string) error {
		calls = append(calls, replicaID)
		if replicaID == "bad" {
			return injected
		}
		return nil
	}
	s := newTestScheduler(t, Config{Binder: binder},
		[]Node{{ID: "n-a", Zone: "a", Slots: 1}})
	if err := s.AddGroup(Group{ID: "g", Skew: 10}); err != nil {
		t.Fatal(err)
	}

	p := mustSchedule(t, s, "g", "bad")
	if used, _ := s.UsedSlots(p.NodeID); used != 1 {
		t.Fatalf("reserved slots = %d, want 1", used)
	}
	if err := s.Bind("g", "bad"); !errors.Is(err, injected) {
		t.Fatalf("bind err = %v, want injected failure", err)
	}
	if used, _ := s.UsedSlots(p.NodeID); used != 0 {
		t.Fatalf("slots after failed bind = %d, want 0", used)
	}
	counts, _ := s.ZoneCounts("g")
	if counts["a"] != 0 {
		t.Fatalf("count after failed bind = %v, want 0", counts)
	}
	reps, _ := s.Replicas("g")
	if reps["bad"].State != "released" {
		t.Fatalf("state = %s, want released", reps["bad"].State)
	}
	if err := s.Bind("g", "bad"); ErrReason(err) != ReasonAlreadyReleased {
		t.Fatalf("want already_released, got %v", err)
	}
	// 释放后槽位可再次使用。
	mustSchedule(t, s, "g", "good")
	if err := s.Bind("g", "good"); err != nil {
		t.Fatalf("bind good: %v", err)
	}
	if err := s.Bind("g", "good"); ErrReason(err) != ReasonAlreadyBound {
		t.Fatalf("want already_bound, got %v", err)
	}
	if len(calls) != 2 || calls[0] != "bad" || calls[1] != "good" {
		t.Fatalf("binder calls = %v, want [bad good]", calls)
	}
}

func TestBindUnknownReservation(t *testing.T) {
	s := newTestScheduler(t, Config{}, []Node{{ID: "n-a", Zone: "a", Slots: 1}})
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("g", "ghost"); ErrReason(err) != ReasonUnknownReservation {
		t.Fatalf("want unknown_reservation, got %v", err)
	}
}

func TestDeleteDoesNotRebalance(t *testing.T) {
	nodes := []Node{
		{ID: "n-a", Zone: "a", Slots: 4},
		{ID: "n-b", Zone: "b", Slots: 4},
	}
	s := newTestScheduler(t, Config{}, nodes)
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	place := func(rid string) string {
		p := mustSchedule(t, s, "g", rid)
		if err := s.Bind("g", rid); err != nil {
			t.Fatalf("bind %s: %v", rid, err)
		}
		return p.Zone
	}
	zones := []string{place("r1"), place("r2"), place("r3")}
	fromA, fromB := 0, 0
	for _, z := range zones {
		if z == "a" {
			fromA++
		} else {
			fromB++
		}
	}
	if fromA != 2 || fromB != 1 {
		t.Fatalf("placements = %v, want 2 in a, 1 in b", zones)
	}

	// 删除 a 区一个副本（a=1,b=1）；随后两个副本必须继续受偏斜约束，
	// 且已存在的副本位置不会被搬动（无任何重新平衡动作）。
	var deletedNode string
	for _, rid := range []string{"r1", "r2", "r3"} {
		reps, _ := s.Replicas("g")
		if reps[rid].Zone == "a" {
			deletedNode = reps[rid].NodeID
			if err := s.DeleteReplica("g", rid); err != nil {
				t.Fatalf("delete: %v", err)
			}
			break
		}
	}
	if used, _ := s.UsedSlots(deletedNode); used != 1 {
		t.Fatalf("used on deleted node = %d, want 1 (no rebalance)", used)
	}
	before, _ := s.Replicas("g")
	mustSchedule(t, s, "g", "r4")
	mustSchedule(t, s, "g", "r5")
	after, _ := s.Replicas("g")
	for id, st := range before {
		if after[id].NodeID != st.NodeID {
			t.Fatalf("replica %s moved %s -> %s: rebalance happened",
				id, st.NodeID, after[id].NodeID)
		}
	}
	counts, _ := s.ZoneCounts("g")
	diff := counts["a"] - counts["b"]
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Fatalf("counts = %v, skew diff %d > 1", counts, diff)
	}

	// 删除不存在的副本被拒绝，状态不变。
	if err := s.DeleteReplica("g", "r1"); ErrReason(err) != ReasonUnknownReplica {
		t.Fatalf("want unknown_replica on second delete, got %v", err)
	}
	if err := s.DeleteReplica("no-group", "x"); ErrReason(err) != ReasonUnknownGroup {
		t.Fatalf("want unknown_group, got %v", err)
	}
}

func TestDeleteReservedFreesSlot(t *testing.T) {
	s := newTestScheduler(t, Config{}, []Node{{ID: "n-a", Zone: "a", Slots: 1}})
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	p := mustSchedule(t, s, "g", "r1")
	if err := s.DeleteReplica("g", "r1"); err != nil {
		t.Fatal(err)
	}
	if used, _ := s.UsedSlots(p.NodeID); used != 0 {
		t.Fatalf("slots = %d, want 0", used)
	}
	// 删除后可重新使用同一副本标识。
	if _, err := s.Schedule("g", "r1"); err != nil {
		t.Fatalf("reschedule after delete: %v", err)
	}
}

func TestDeleteRacesWithInflightBind(t *testing.T) {
	enter := make(chan struct{})
	release := make(chan struct{})
	binder := func(string, string, string) error {
		close(enter)
		<-release
		return errors.New("bind too late")
	}
	s := newTestScheduler(t, Config{Binder: binder},
		[]Node{{ID: "n-a", Zone: "a", Slots: 1}})
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	p := mustSchedule(t, s, "g", "r1")

	var wg sync.WaitGroup
	wg.Add(2)
	var bindErr, delErr error
	go func() { defer wg.Done(); bindErr = s.Bind("g", "r1") }()
	<-enter
	go func() { defer wg.Done(); delErr = s.DeleteReplica("g", "r1") }()
	// 给删除一点时间阻塞在 bindMu 上，再释放绑定函数。
	close(release)
	wg.Wait()

	if bindErr == nil || delErr != nil {
		t.Fatalf("bindErr=%v delErr=%v, want bind failure and successful delete",
			bindErr, delErr)
	}
	if used, _ := s.UsedSlots(p.NodeID); used != 0 {
		t.Fatalf("slots = %d, want 0 after bind failure + delete", used)
	}
	if reps, _ := s.Replicas("g"); len(reps) != 0 {
		t.Fatalf("replicas = %v, want empty after delete", reps)
	}
}
