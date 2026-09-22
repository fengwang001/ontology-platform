package store

import (
	"bytes"
	"fmt"
	"testing"

	"ontology/snapshot"
)

// readAll 记录若干快照对若干键的全部读结果。
func readAll(s *Store, snaps []*snapshot.Snapshot, keys []string) [][]byte {
	var out [][]byte
	for _, snap := range snaps {
		for _, key := range keys {
			v, lk := s.ReadAt(snap, key)
			out = append(out, []byte(fmt.Sprintf("%s=%d:%s", key, lk, v)))
		}
	}
	return out
}

// 第 5 条：回收前后，所有活跃快照的读结果逐字节相同。
func TestReclaimPreservesActiveSnapshotReads(t *testing.T) {
	s := newStore(t, Config{})
	keys := []string{"a", "b", "c"}
	// 先提交三轮（无快照），产生被遮蔽的旧版本。
	for round := 0; round < 3; round++ {
		for _, k := range keys {
			commitKV(t, s, k, fmt.Sprintf("%s-v%d", k, round))
		}
	}
	// 再交替开快照与提交，让不同快照看到不同代。
	var snaps []*snapshot.Snapshot
	for round := 3; round < 6; round++ {
		snap, err := s.Begin()
		if err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, snap)
		for _, k := range keys {
			commitKV(t, s, k, fmt.Sprintf("%s-v%d", k, round))
		}
	}
	defer func() {
		for _, snap := range snaps {
			s.Release(snap)
		}
	}()

	before := readAll(s, snaps, keys)
	reclaimed := s.Reclaim()
	if reclaimed == 0 {
		t.Fatalf("应回收到旧版本")
	}
	after := readAll(s, snaps, keys)

	if len(before) != len(after) {
		t.Fatalf("读结果数量变化")
	}
	for i := range before {
		if !bytes.Equal(before[i], after[i]) {
			t.Fatalf("回收前后读结果不同: %q vs %q", before[i], after[i])
		}
	}
	if err := s.checkConsistency(); err != nil {
		t.Fatalf("回收后一致性: %v", err)
	}
}

// 第 6 条：增量回收。N 个键每键 M 个版本，只有 K 个热键的版本
// 在水位之下可回收；考察数不随 N 线性增长。
func examinedForN(t *testing.T, n, k, m int) int64 {
	t.Helper()
	s := newStore(t, Config{})
	// 第一阶段：K 个热键写满 M 个版本（事务号较小）。
	for round := 0; round < m; round++ {
		for i := 0; i < k; i++ {
			commitKV(t, s, fmt.Sprintf("hot-%d", i), fmt.Sprintf("v%d", round))
		}
	}
	// 钉住水位：此时快照点之前的提交才可能被回收。
	snap, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release(snap)
	// 第二阶段：其余 N-K 个键各写 M 个版本（事务号较大，不可回收）。
	for round := 0; round < m; round++ {
		for i := 0; i < n-k; i++ {
			commitKV(t, s, fmt.Sprintf("cold-%d", i), fmt.Sprintf("v%d", round))
		}
	}
	before := s.Stats().Examined
	s.Reclaim()
	return s.Stats().Examined - before
}

func TestReclaimExaminedIndependentOfN(t *testing.T) {
	const k, m = 5, 10
	e100 := examinedForN(t, 100, k, m)
	e10000 := examinedForN(t, 10000, k, m)
	t.Logf("N=100 考察数=%d, N=10000 考察数=%d", e100, e10000)
	if e100 != e10000 {
		t.Fatalf("考察数不应随 N 增长: N=100 为 %d, N=10000 为 %d", e100, e10000)
	}
	want := int64(k * (m - 1)) // 每个热键 M-1 个被遮蔽版本
	if e100 != want {
		t.Fatalf("考察数应等于可回收候选数 %d，得到 %d", want, e100)
	}
}

// 第 7 条：水位只升不降；并发开关快照时新快照读不到已回收版本。
func TestWaterLevelMonotonicAndNoRaceWithNewSnapshots(t *testing.T) {
	s := newStore(t, Config{})
	for round := 0; round < 3; round++ {
		commitKV(t, s, "k", fmt.Sprintf("v%d", round))
	}
	s.Reclaim()
	w1 := s.Stats().WaterLevel

	snap, _ := s.Begin() // 打开快照压低瞬时视野
	s.Reclaim()
	w2 := s.Stats().WaterLevel
	s.Release(snap)
	s.Reclaim()
	w3 := s.Stats().WaterLevel

	if !(w1 <= w2 && w2 <= w3) {
		t.Fatalf("水位只升不降: %v %v %v", w1, w2, w3)
	}

	// 水位推过之后新打开的快照，读到的必须是未被回收破坏的结果。
	snap2, _ := s.Begin()
	defer s.Release(snap2)
	if got, lk := s.ReadAt(snap2, "k"); lk != LookupFound || string(got) != "v2" {
		t.Fatalf("新快照应读到最新值 v2，得到 %q/%v", got, lk)
	}
}
