package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 占用期间大量并发普通更新：全部以 RejectInstanceOccupied 拒绝，版本号不变。
func TestConcurrentUpdatesDuringOccupancy(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ, _ := s.Acquire("A1", "T1")
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Update(CallerID(fmt.Sprintf("c%d", i)), "T1", 1,
				Mutation{Property: "title", Value: fmt.Sprintf("v%d", i)})
			var re *RejectError
			if !errors.As(err, &re) || re.Code != RejectInstanceOccupied {
				t.Errorf("update %d: got %v, want instance_occupied", i, err)
			}
		}(i)
	}
	wg.Wait()
	snap, _ := s.Get("T1")
	if snap.Version != 1 {
		t.Fatalf("rejected updates bumped version to %d", snap.Version)
	}
	occ.Abort()
}

// 占用刚释放瞬间的更新竞争：携带旧版本的更新要么撞上占用（RejectInstanceOccupied），
// 要么撞上释放后的新版本（RejectVersionStale），绝不允许成功；
// 携带释放后新版本的更新在释放完成后必须成功。
func TestUpdateRacesWithRelease(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		s := newTestStore(t)
		mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a", "state": "open"})

		occ, _ := s.Acquire("A1", "T1")
		occ.Apply(Mutation{Property: "state", Value: "closed"})

		var wg sync.WaitGroup
		results := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, results[0] = s.Update("c-stale", "T1", 1, Mutation{Property: "title", Value: "x"})
		}()
		go func() {
			defer wg.Done()
			_, err := occ.Commit()
			results[1] = err
		}()
		wg.Wait()

		if results[1] != nil {
			t.Fatalf("trial %d: commit failed: %v", trial, results[1])
		}
		var re *RejectError
		if !errors.As(results[0], &re) {
			t.Fatalf("trial %d: stale update unexpectedly succeeded", trial)
		}
		if re.Code != RejectInstanceOccupied && re.Code != RejectVersionStale {
			t.Fatalf("trial %d: stale update rejected with unexpected code %s", trial, re.Code)
		}
		snap, _ := s.Get("T1")
		if snap.Version != 2 || snap.Properties["state"] != "closed" || snap.Properties["title"] != "a" {
			t.Fatalf("trial %d: inconsistent post-release state: %+v", trial, snap)
		}
		// 释放完成后按最新版本发起的更新必须成功。
		if _, err := s.Update("c-fresh", "T1", 2, Mutation{Property: "title", Value: "y"}); err != nil {
			t.Fatalf("trial %d: update with latest version after release: %v", trial, err)
		}
	}
}

// 乐观更新与独占动作交替执行：最终状态必须等价于某个串行顺序，
// 且占用期间不允许插入任何普通更新（由审计日志验证）。
func TestAlternatingOptimisticAndExclusive(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a", "state": "open"})

	expected := uint64(1)
	for round := 0; round < 20; round++ {
		v, err := s.Update("c1", "T1", expected, Mutation{Property: "title", Value: fmt.Sprintf("t%d", round)})
		if err != nil || v != expected+1 {
			t.Fatalf("round %d update: v=%d err=%v", round, v, err)
		}
		expected = v

		occ, err := s.Acquire(ActionID(fmt.Sprintf("A%d", round)), "T1")
		if err != nil {
			t.Fatalf("round %d acquire: %v", round, err)
		}
		occ.Apply(Mutation{Property: "state", Value: fmt.Sprintf("s%d", round)})
		v, err = occ.Commit()
		if err != nil || v != expected+1 {
			t.Fatalf("round %d commit: v=%d err=%v", round, v, err)
		}
		expected = v
	}
	snap, _ := s.Get("T1")
	if snap.Version != expected {
		t.Fatalf("final version %d, want %d", snap.Version, expected)
	}
	// 审计验证：占用窗口（acquire 成功到 commit/abort）之间不存在成功的普通更新。
	occupied := false
	for _, rec := range s.Audit().Records() {
		switch rec.Kind {
		case OpAcquire:
			if rec.Code == RejectNone {
				occupied = true
			}
		case OpCommit, OpAbort:
			occupied = false
		case OpUpdate:
			if occupied && rec.Code == RejectNone {
				t.Fatalf("seq %d: normal update succeeded inside occupancy window", rec.Seq)
			}
		}
	}
}
