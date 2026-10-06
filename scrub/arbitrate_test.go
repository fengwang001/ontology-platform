package scrub

import (
	"reflect"
	"testing"
)

func rep(node, version uint64, stored, actual string) Replica {
	return Replica{NodeID: node, Version: version, Stored: stored, Actual: actual}
}

func TestQuorum(t *testing.T) {
	want := map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3}
	for n, q := range want {
		if got := Quorum(n); got != q {
			t.Errorf("Quorum(%d) = %d, want %d", n, got, q)
		}
	}
}

// 已提交版本恰等于最大自洽版本：可修复，低版本自洽副本进入修复目标。
func TestArbitrateCommittedEqualsMaxSelf(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 5, "a", "a"),
		rep(2, 5, "a", "a"),
		rep(3, 4, "a", "a"),
	})
	if v.Outcome != OutcomeRepaired {
		t.Fatalf("outcome = %v, want Repaired", v.Outcome)
	}
	if v.CommittedVersion != 5 || v.AuthVersion != 5 || v.AuthDigest != "a" {
		t.Fatalf("committed=%d auth=(%d,%q), want 5/(5,a)", v.CommittedVersion, v.AuthVersion, v.AuthDigest)
	}
	if !reflect.DeepEqual(v.RepairTargets, []uint64{3}) {
		t.Fatalf("targets = %v, want [3]", v.RepairTargets)
	}
}

// 已提交版本低于最大自洽版本：未提交写入向前提交。
func TestArbitrateForwardCommit(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 7, "x", "x"),
		rep(2, 3, "a", "a"),
		rep(3, 3, "a", "a"),
	})
	if v.Outcome != OutcomeRepaired {
		t.Fatalf("outcome = %v, want Repaired", v.Outcome)
	}
	if v.CommittedVersion != 3 || v.AuthVersion != 7 || v.AuthDigest != "x" {
		t.Fatalf("committed=%d auth=(%d,%q), want 3/(7,x)", v.CommittedVersion, v.AuthVersion, v.AuthDigest)
	}
	if !reflect.DeepEqual(v.RepairTargets, []uint64{2, 3}) {
		t.Fatalf("targets = %v, want [2 3]", v.RepairTargets)
	}
}

// 最高版本副本全部位腐：已提交数据丢失。
func TestArbitrateHighestVersionsAllCorrupt(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 5, "a", "rot1"),
		rep(2, 5, "a", "rot2"),
		rep(3, 4, "a", "a"),
	})
	if v.Outcome != OutcomeCommittedDataLost {
		t.Fatalf("outcome = %v, want CommittedDataLost", v.Outcome)
	}
	if v.CommittedVersion != 5 || v.AuthVersion != 4 {
		t.Fatalf("committed=%d maxSelf=%d, want 5/4", v.CommittedVersion, v.AuthVersion)
	}
	if len(v.RepairTargets) != 0 {
		t.Fatalf("unrepairable verdict must not carry targets, got %v", v.RepairTargets)
	}
}

// 同版本不同摘要：版本冲突。
func TestArbitrateVersionConflict(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 5, "a", "a"),
		rep(2, 5, "b", "b"),
		rep(3, 1, "a", "a"),
	})
	if v.Outcome != OutcomeVersionConflict {
		t.Fatalf("outcome = %v, want VersionConflict", v.Outcome)
	}
}

// 没有任何自洽副本：无可用来源。
func TestArbitrateNoSource(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 5, "a", "rot1"),
		rep(2, 5, "a", "rot2"),
	})
	if v.Outcome != OutcomeNoSource {
		t.Fatalf("outcome = %v, want NoSource", v.Outcome)
	}
	if v.CommittedVersion != 5 {
		t.Fatalf("committed = %d, want 5", v.CommittedVersion)
	}
}

// 位腐副本版本高于权威版本：仍属修复目标，修复后版本回落到权威。
func TestArbitrateCorruptHigherVersionRepairedDown(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 9, "z", "rot"),
		rep(2, 5, "a", "a"),
		rep(3, 5, "a", "a"),
		rep(4, 5, "a", "a"),
		rep(5, 5, "a", "a"),
	})
	if v.Outcome != OutcomeRepaired {
		t.Fatalf("outcome = %v, want Repaired", v.Outcome)
	}
	if v.CommittedVersion != 5 || v.AuthVersion != 5 {
		t.Fatalf("committed=%d auth=%d, want 5/5", v.CommittedVersion, v.AuthVersion)
	}
	if !reflect.DeepEqual(v.RepairTargets, []uint64{1}) {
		t.Fatalf("targets = %v, want [1]", v.RepairTargets)
	}
}

// 全部一致：无需修复。
func TestArbitrateConsistent(t *testing.T) {
	v := Arbitrate([]Replica{
		rep(1, 5, "a", "a"),
		rep(2, 5, "a", "a"),
		rep(3, 5, "a", "a"),
	})
	if v.Outcome != OutcomeConsistent {
		t.Fatalf("outcome = %v, want Consistent", v.Outcome)
	}
	if len(v.RepairTargets) != 0 {
		t.Fatalf("targets = %v, want empty", v.RepairTargets)
	}
}

// 不可修复三类的判定次序：无来源优先于丢失与冲突。
func TestArbitrateUnrepairablePrecedence(t *testing.T) {
	// 全部位腐且版本各异：既无来源也"看似"丢失，只报无来源。
	v := Arbitrate([]Replica{
		rep(1, 9, "a", "r1"),
		rep(2, 8, "b", "r2"),
		rep(3, 7, "c", "r3"),
	})
	if v.Outcome != OutcomeNoSource {
		t.Fatalf("outcome = %v, want NoSource", v.Outcome)
	}
	// 有自洽副本但低于已提交版本，且该版本摘要冲突：只报丢失。
	v = Arbitrate([]Replica{
		rep(1, 9, "a", "rot1"),
		rep(2, 9, "a", "rot2"),
		rep(3, 9, "a", "rot3"),
		rep(4, 5, "x", "x"),
		rep(5, 5, "y", "y"),
	})
	if v.Outcome != OutcomeCommittedDataLost {
		t.Fatalf("outcome = %v, want CommittedDataLost (maxSelf=5 < committed=9)", v.Outcome)
	}
}
