package scrub_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/scrub"
	"ontology/scrub/naivemodel"
)

type opKind int

const (
	opCreate opKind = iota
	opWrite
	opRot
	opDiscard
	opPatrol
	opDue
)

type op struct {
	kind     opKind
	at       int64
	block    int
	version  int64
	digest2  string
	nodes    []int
	fail     map[int]bool
	limit    int
	replicas []naivemodel.Replica
	interval int64
	digest   string
}

func digestOf(v int64, salt int) string {
	return fmt.Sprintf("d-v%d-s%d", v, salt)
}

// genSequence 生成时间戳非递减的随机操作序列；块池 0..numBlocks-1，节点 1..5。
func genSequence(rng *rand.Rand, steps, numBlocks int) []op {
	var ops []op
	clock := int64(0)
	existing := map[int]bool{}

	advance := func() int64 {
		if rng.Intn(3) == 0 {
			clock += int64(rng.Intn(6))
		}
		return clock
	}
	makeCreate := func(id int) op {
		n := 2 + rng.Intn(4)
		perm := rng.Perm(5)[:n]
		v := int64(1 + rng.Intn(3))
		reps := make([]naivemodel.Replica, n)
		for j, node := range perm {
			nodeID := node + 1
			reps[j] = naivemodel.Replica{
				Node: nodeID, Version: v,
				SavedDigest: digestOf(v, 0), ActualDigest: digestOf(v, 0),
			}
		}
		return op{
			kind: opCreate, at: advance(), block: id,
			replicas: reps, interval: int64(2 + rng.Intn(8)),
		}
	}

	for i := 0; i < steps; i++ {
		if len(existing) < numBlocks && rng.Intn(3) != 0 {
			id := rng.Intn(numBlocks)
			if !existing[id] {
				ops = append(ops, makeCreate(id))
				existing[id] = true
				continue
			}
		}

		id := rng.Intn(numBlocks)
		switch opKind(rng.Intn(int(opDue) + 1)) {
		case opWrite:
			n := 1 + rng.Intn(3)
			perm := rng.Perm(5)[:n]
			ns := make([]int, n)
			for j, x := range perm {
				ns[j] = x + 1
			}
			ops = append(ops, op{
				kind: opWrite, at: advance(), block: id,
				version: int64(1 + rng.Intn(6)),
				digest:  digestOf(int64(1+rng.Intn(6)), rng.Intn(3)),
				nodes:   ns,
			})
		case opRot:
			ops = append(ops, op{
				kind: opRot, at: advance(), block: id,
				digest2: fmt.Sprintf("ROT-%d", rng.Intn(100000)),
				nodes:   []int{1 + rng.Intn(5)},
			})
		case opDiscard:
			ops = append(ops, op{
				kind: opDiscard, at: advance(), block: id,
				nodes: []int{1 + rng.Intn(5)},
			})
		case opPatrol:
			fail := map[int]bool{}
			for n := 1; n <= 5; n++ {
				if rng.Intn(3) == 0 {
					fail[n] = true
				}
			}
			ops = append(ops, op{kind: opPatrol, at: advance(), block: id, fail: fail})
		case opDue:
			ops = append(ops, op{kind: opDue, at: advance(), limit: 1 + rng.Intn(numBlocks+2)})
		case opCreate:
			id2 := rng.Intn(numBlocks)
			if !existing[id2] {
				ops = append(ops, makeCreate(id2))
				existing[id2] = true
			}
		}
	}
	return ops
}

func mapErr(err error) naivemodel.ErrorClass {
	switch {
	case err == nil:
		return 0
	case errString(err) == scrub.ErrInvalid.Error():
		return naivemodel.ErrInvalid
	case errString(err) == scrub.ErrClockBack.Error():
		return naivemodel.ErrClockBack
	case errString(err) == scrub.ErrNotFound.Error():
		return naivemodel.ErrNotFound
	case errString(err) == scrub.ErrTooFrequent.Error():
		return naivemodel.ErrTooFrequent
	default:
		return -1
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func opName(k opKind) string {
	switch k {
	case opCreate:
		return "CREATE"
	case opWrite:
		return "WRITE"
	case opRot:
		return "ROT"
	case opDiscard:
		return "DISCARD"
	case opPatrol:
		return "PATROL"
	default:
		return "DUE"
	}
}

// TestDifferentialRandom 与朴素模型对照多条随机序列；
// 日志逐行记录输入、输出与判定依据（输出到 SCRUB_DIFF_LOG 或临时目录）。
func TestDifferentialRandom(t *testing.T) {
	logDir := os.Getenv("SCRUB_DIFF_LOG")
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "scrub-diff-logs")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, seed := range []int64{1, 2, 3, 7, 42, 99, 2026} {
		seed := seed
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			numBlocks := 4 + rng.Intn(4)
			ops := genSequence(rng, 400, numBlocks)

			logPath := filepath.Join(logDir, fmt.Sprintf("seed%d.log", seed))
			lf, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer lf.Close()
			w := bufio.NewWriter(lf)
			defer w.Flush()

			s := scrub.NewStore()
			m := naivemodel.New()
			for idx, o := range ops {
				replayAndCompare(t, w, idx, o, s, m)
			}
			t.Logf("seed %d: %d ops replayed, full log at %s", seed, len(ops), logPath)
		})
	}
}

func replayAndCompare(t *testing.T, w *bufio.Writer, idx int, o op, s *scrub.Store, m *naivemodel.Model) {
	t.Helper()
	fmt.Fprintf(w, "\n[op %04d] %s at=%d", idx, opName(o.kind), o.at)

	switch o.kind {
	case opCreate:
		sreps := make([]scrub.Replica, len(o.replicas))
		for i, r := range o.replicas {
			sreps[i] = scrub.Replica{
				Node: r.Node, Version: r.Version,
				SavedDigest: r.SavedDigest, ActualDigest: r.ActualDigest,
			}
		}
		fmt.Fprintf(w, " block=%d n=%d interval=%d", o.block, len(sreps), o.interval)
		e1 := s.CreateBlock(scrub.Time(o.at), o.block, sreps, scrub.Time(o.interval))
		e2 := m.CreateBlock(naivemodel.Time(o.at), o.block, o.replicas, naivemodel.Time(o.interval))
		fmt.Fprintf(w, "\n  -> impl=%v naive=%v", e1, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d create mismatch: impl=%v naive=%v", idx, e1, e2)
		}

	case opWrite:
		fmt.Fprintf(w, " block=%d v=%d digest=%q nodes=%v", o.block, o.version, o.digest, o.nodes)
		e1 := s.Write(scrub.Time(o.at), o.block, o.version, o.digest, o.nodes)
		e2 := m.Write(naivemodel.Time(o.at), o.block, o.version, o.digest, o.nodes)
		fmt.Fprintf(w, "\n  -> impl=%v naive=%v", e1, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d write mismatch: impl=%v naive=%v", idx, e1, e2)
		}

	case opRot:
		node := o.nodes[0]
		fmt.Fprintf(w, " block=%d node=%d corruptDigest=%q", o.block, node, o.digest2)
		e1 := s.Rot(scrub.Time(o.at), o.block, node, o.digest2)
		e2 := m.Rot(naivemodel.Time(o.at), o.block, node, o.digest2)
		fmt.Fprintf(w, "\n  -> impl=%v naive=%v", e1, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d rot mismatch: impl=%v naive=%v", idx, e1, e2)
		}

	case opDiscard:
		node := o.nodes[0]
		fmt.Fprintf(w, " block=%d node=%d", o.block, node)
		e1 := s.Discard(scrub.Time(o.at), o.block, node)
		e2 := m.Discard(naivemodel.Time(o.at), o.block, node)
		fmt.Fprintf(w, "\n  -> impl=%v naive=%v", e1, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d discard mismatch: impl=%v naive=%v", idx, e1, e2)
		}

	case opPatrol:
		fmt.Fprintf(w, " block=%d failSet=%v", o.block, sortedFail(o.fail))
		r1, e1 := s.Patrol(scrub.Time(o.at), o.block, o.fail)
		r2, e2 := m.Patrol(naivemodel.Time(o.at), o.block, o.fail)
		fmt.Fprintf(w, "\n  -> impl.err=%v naive.err=%v", e1, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d patrol error mismatch: impl=%v naive=%v", idx, e1, e2)
		}
		if e1 == nil {
			fmt.Fprintf(w, "\n     impl : outcome=%s quorum=%d committed=%d auth=v%d/%q targets=%v repaired=%v failed=%v",
				r1.Outcome, r1.Quorum, r1.CommittedVersion,
				r1.AuthorityVersion, r1.AuthorityDigest,
				targetSummary(r1.Targets), r1.Repaired, r1.FailedNodes)
			fmt.Fprintf(w, "\n     naive: outcome=%s quorum=%d committed=%d auth=v%d/%q targets=%v repaired=%v failed=%v",
				r2.Outcome, r2.Quorum, r2.CommittedVersion,
				r2.AuthorityVersion, r2.AuthorityDigest,
				naiveTargetSummary(r2.Targets), r2.Repaired, r2.FailedNodes)
			if scrub.Outcome(r2.Outcome) != r1.Outcome ||
				r1.Quorum != r2.Quorum ||
				r1.CommittedVersion != r2.CommittedVersion ||
				r1.AuthorityVersion != r2.AuthorityVersion ||
				r1.AuthorityDigest != r2.AuthorityDigest ||
				!sameIntSet(r1.Repaired, r2.Repaired) ||
				!sameIntSet(r1.FailedNodes, r2.FailedNodes) {
				t.Fatalf("op %d patrol result mismatch:\nimpl=%+v\nnaive=%+v", idx, r1, r2)
			}
		}

	case opDue:
		fmt.Fprintf(w, " limit=%d", o.limit)
		g1, e1 := s.Due(scrub.Time(o.at), o.limit)
		g2, e2 := m.Due(naivemodel.Time(o.at), o.limit)
		fmt.Fprintf(w, "\n  -> impl=%v(%v) naive=%v(%v) basis=never-first,last-asc,id-asc",
			g1, e1, g2, e2)
		if mapErr(e1) != e2 {
			t.Fatalf("op %d due error mismatch: impl=%v naive=%v", idx, e1, e2)
		}
		if e1 == nil && !sameSeq(g1, g2) {
			t.Fatalf("op %d due order mismatch: impl=%v naive=%v", idx, g1, g2)
		}
	}

	for b := 0; b < 8; b++ {
		sr, sok := replicaSnapshot(s, b)
		nr, nok := m.Replicas(b)
		if sok != nok {
			t.Fatalf("op %d block %d existence mismatch impl=%v naive=%v", idx, b, sok, nok)
		}
		if sok && !replicasEqual(sr, nr) {
			t.Fatalf("op %d block %d state mismatch\nimpl=%v\nnaive=%v", idx, b, sr, nr)
		}
	}
	a1, a2 := s.Alerts(), m.Alerts()
	if len(a1) != len(a2) {
		t.Fatalf("op %d alert length mismatch %d vs %d", idx, len(a1), len(a2))
	}
	for i := range a1 {
		if a1[i].BlockID != a2[i].BlockID || scrub.Outcome(a2[i].Outcome) != a1[i].Outcome ||
			int64(a1[i].Time) != int64(a2[i].Time) {
			t.Fatalf("op %d alert %d mismatch: %+v vs %+v", idx, i, a1[i], a2[i])
		}
	}
}
