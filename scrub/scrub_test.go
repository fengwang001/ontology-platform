package scrub

import "testing"

func rep(node int, v int64, digest string) Replica {
	return Replica{Node: node, Version: v, SavedDigest: digest, ActualDigest: digest}
}

func rotten(r Replica, actual string) Replica {
	r.ActualDigest = actual
	return r
}

func targetNodes(a Arbitration) map[int]bool {
	m := make(map[int]bool, len(a.Targets))
	for _, t := range a.Targets {
		m[t.Node] = true
	}
	return m
}

// TestQuorum2to5 覆盖副本数 2..5 各自的法定数（n/2 向下取整 + 1）。
func TestQuorum2to5(t *testing.T) {
	cases := []struct{ n, quorum int }{{2, 2}, {3, 2}, {4, 3}, {5, 3}}
	for _, c := range cases {
		rs := make([]Replica, c.n)
		for i := range rs {
			rs[i] = rep(i+1, 1, "d1")
		}
		a := Arbitrate(rs)
		if a.Quorum != c.quorum {
			t.Fatalf("n=%d quorum=%d want %d", c.n, a.Quorum, c.quorum)
		}
		if a.Outcome != OutcomeNoRepair {
			t.Fatalf("n=%d outcome=%s want no-repair", c.n, a.Outcome)
		}
	}
}

// TestCommittedEqualsMaxIntact：已提交版本恰等于最大自洽版本。
func TestCommittedEqualsMaxIntact(t *testing.T) {
	// 3 副本 quorum=2：版本 3,3,2，已提交=3；位腐 v3 由另一个 v3 修复。
	rs := []Replica{
		rep(1, 3, "d3"),
		rotten(rep(2, 3, "d3"), "XXX"),
		rep(3, 2, "d2"),
	}
	a := Arbitrate(rs)
	if a.CommittedVersion != 3 || a.AuthorityVersion != 3 || a.AuthorityDigest != "d3" {
		t.Fatalf("unexpected arbitration: %+v", a)
	}
	if a.Outcome != OutcomeRepaired {
		t.Fatalf("outcome=%s want repaired", a.Outcome)
	}
	tg := targetNodes(a)
	if !tg[2] || !tg[3] || tg[1] {
		t.Fatalf("targets should be nodes 2(bitrot) and 3(stale), got %v", tg)
	}
	if a.Targets[0].Kind != RepairBitrot {
		t.Fatal("node 2 must be classified bitrot")
	}
}

// TestForwardCommit：最大自洽版本高于已提交版本 -> 未提交写入向前提交。
func TestForwardCommit(t *testing.T) {
	rs := []Replica{rep(1, 4, "d4"), rep(2, 3, "d3"), rep(3, 3, "d3")}
	a := Arbitrate(rs)
	if a.CommittedVersion != 3 {
		t.Fatalf("committed=%d want 3", a.CommittedVersion)
	}
	if a.Outcome != OutcomeRepaired || a.AuthorityVersion != 4 {
		t.Fatalf("want forward commit to v4, got %+v", a)
	}
	if targetNodes(a)[1] {
		t.Fatal("v4 node must not be repaired")
	}
}

// TestTopVersionAllRotten：最高版本副本全部位腐，最大自洽版本低于
// 已提交版本 -> 已提交数据丢失。
func TestTopVersionAllRotten(t *testing.T) {
	rs := []Replica{
		rotten(rep(1, 5, "d5"), "bad1"),
		rotten(rep(2, 5, "d5"), "bad2"),
		rep(3, 4, "d4"),
	}
	a := Arbitrate(rs)
	if a.Outcome != OutcomeCommittedLost {
		t.Fatalf("outcome=%s want committed-data-lost", a.Outcome)
	}
}

// TestSameVersionDifferentDigest：同版本不同摘要 -> 版本冲突。
func TestSameVersionDifferentDigest(t *testing.T) {
	rs := []Replica{
		rep(1, 3, "alpha"),
		rep(2, 3, "beta"),
		rep(3, 2, "old"),
	}
	a := Arbitrate(rs)
	if a.Outcome != OutcomeConflict {
		t.Fatalf("outcome=%s want version-conflict", a.Outcome)
	}
}

// TestNoIntactReplica：全部位腐 -> 无可用来源。
func TestNoIntactReplica(t *testing.T) {
	rs := []Replica{
		rotten(rep(1, 3, "d3"), "x"),
		rotten(rep(2, 3, "d3"), "y"),
	}
	a := Arbitrate(rs)
	if a.Outcome != OutcomeNoSource {
		t.Fatalf("outcome=%s want no-source", a.Outcome)
	}
}

// TestConflictPriority：最大自洽不低于已提交但摘要冲突，须报冲突而非丢失。
func TestConflictPriority(t *testing.T) {
	rs := []Replica{
		rep(1, 3, "alpha"),
		rep(2, 3, "beta"),
		rotten(rep(3, 5, "d5"), "zz"),
	}
	a := Arbitrate(rs)
	if a.Outcome != OutcomeConflict {
		t.Fatalf("outcome=%s want conflict", a.Outcome)
	}
}

// TestSingleReplica：丢弃到只剩 1 个副本仍可巡检并自洽判定。
func TestSingleReplica(t *testing.T) {
	a := Arbitrate([]Replica{rep(1, 9, "d9")})
	if a.Quorum != 1 || a.CommittedVersion != 9 || a.Outcome != OutcomeNoRepair {
		t.Fatalf("single intact replica: %+v", a)
	}
	a = Arbitrate([]Replica{rotten(rep(1, 9, "d9"), "bad")})
	if a.Outcome != OutcomeNoSource {
		t.Fatalf("single rotten replica: %s", a.Outcome)
	}
}
