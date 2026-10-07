package audit_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/audit"
)

// 并发混合调用：任意并发执行的结果必须等价于某个串行顺序。
// 验证手段：追加式序列（版本/记录）的序号在并发后仍然
// 连续无空洞且无重复——这是可线性化的直接可观测证据。
func TestConcurrentMixedOps(t *testing.T) {
	s := audit.New()
	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	recIDs := make([][]audit.RecordID, workers)
	verIDs := make([][]audit.VersionID, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				v, err := s.SubmitRuleVersion(allowAliceRules())
				if err != nil {
					t.Error(err)
					return
				}
				verIDs[w] = append(verIDs[w], v)
				rec, err := s.RecordAccess("alice", "doc-1", readReq(),
					t0.Add(time.Duration(w*perWorker+i)*time.Second), v, audit.DecisionAllow)
				if err != nil {
					t.Error(err)
					return
				}
				recIDs[w] = append(recIDs[w], rec)
				if _, _, err := s.Replay(rec); err != nil {
					t.Error(err)
					return
				}
				if _, _, _, err := s.QueryLegality("alice", "doc-1", t0.Add(time.Duration(w*perWorker+i)*time.Second)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	assertSeqContiguous := func(kind string, n int, get func(int) uint64) {
		seen := make(map[uint64]bool)
		for i := 0; i < n; i++ {
			seq := get(i)
			if seen[seq] {
				t.Fatalf("%s seq %d duplicated", kind, seq)
			}
			seen[seq] = true
		}
		for seq := uint64(1); seq <= uint64(n); seq++ {
			if !seen[seq] {
				t.Fatalf("%s seq %d missing (hole in append sequence)", kind, seq)
			}
		}
	}

	var allVers, allRecs []string
	for w := 0; w < workers; w++ {
		for _, v := range verIDs[w] {
			allVers = append(allVers, string(v))
		}
		for _, r := range recIDs[w] {
			allRecs = append(allRecs, string(r))
		}
	}
	assertSeqContiguous("version", len(allVers), func(i int) uint64 {
		v, err := s.GetVersion(audit.VersionID(allVers[i]))
		if err != nil {
			t.Fatal(err)
		}
		return v.Seq
	})
	assertSeqContiguous("record", len(allRecs), func(i int) uint64 {
		r, err := s.GetRecord(audit.RecordID(allRecs[i]))
		if err != nil {
			t.Fatal(err)
		}
		return r.Seq
	})
}

// 并发纠正追加：对同一条记录并发热纠，链仍然确定有序，
// 且每次追加要么成功接到链头、要么被确定性地拒绝。
func TestConcurrentCorrections(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionDeny)

	const n = 32
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				chain, err := s.CorrectionsOf(rec)
				if err != nil {
					t.Error(err)
					return
				}
				var head audit.CorrectionID
				if len(chain) > 0 {
					head = chain[len(chain)-1].ID
				}
				_, err = s.AppendCorrection(rec, head, audit.Decision(w%2 == 0), fmt.Sprintf("worker-%d", w))
				if err == nil {
					return
				}
				if !errors.Is(err, audit.ErrCorrectionChain) {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	chain, err := s.CorrectionsOf(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != n {
		t.Fatalf("want %d corrections, got %d", n, len(chain))
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].Supersedes != chain[i-1].ID {
			t.Fatalf("chain broken at %d: %+v", i, chain)
		}
		if chain[i].Seq <= chain[i-1].Seq {
			t.Fatalf("chain order not deterministic: %+v", chain)
		}
	}
}
