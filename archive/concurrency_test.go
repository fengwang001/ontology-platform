package archive_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/archive"
)

// 并发调用等价于某个串行顺序：用锁内钩子记录实际串行化顺序，
// 按该顺序在全新服务上重放，结果必须完全一致。
func TestConcurrentSerializableReplay(t *testing.T) {
	for round := 0; round < 20; round++ {
		s := newSvc(t, testCfg())
		mustOK(t, s.AddVolume(0, "v1", archive.Public), "add v1")
		mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
		for i := 0; i < 8; i++ {
			mustOK(t, s.AddBorrower(0, fmt.Sprintf("u%d", i), archive.TopSecret), "add borrower")
		}
		type record struct {
			op  archive.Op
			res archive.Result
		}
		var records []record
		s.SetHook(func(op archive.Op, res archive.Result) {
			records = append(records, record{op, res})
		})
		var ops []archive.Op
		for i := 0; i < 8; i++ {
			id := fmt.Sprintf("u%d", i)
			ops = append(ops,
				archive.Op{Kind: archive.OpBorrow, Now: 1, BorrowerID: id, VolumeIDs: []string{"v1"}},
				archive.Op{Kind: archive.OpBorrow, Now: 1, BorrowerID: id, VolumeIDs: []string{"v2"}},
				archive.Op{Kind: archive.OpReserve, Now: 2, BorrowerID: id, VolumeID: "v1"},
				archive.Op{Kind: archive.OpReturn, Now: 3, VolumeID: "v1"},
				archive.Op{Kind: archive.OpPickup, Now: 4, BorrowerID: id, VolumeID: "v1"},
				archive.Op{Kind: archive.OpReturn, Now: 5, VolumeID: "v2"},
			)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range ops {
			op := op
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.Apply(op)
			}()
		}
		close(start)
		wg.Wait()
		if len(records) != len(ops) {
			t.Fatalf("round %d: 钩子记录数 %d != 操作数 %d", round, len(records), len(ops))
		}
		// 按记录的串行顺序重放，结果必须逐项一致。
		replay := newSvc(t, testCfg())
		mustOK(t, replay.AddVolume(0, "v1", archive.Public), "replay add v1")
		mustOK(t, replay.AddVolume(0, "v2", archive.Public), "replay add v2")
		for i := 0; i < 8; i++ {
			mustOK(t, replay.AddBorrower(0, fmt.Sprintf("u%d", i), archive.TopSecret), "replay add")
		}
		for i, rec := range records {
			got := replay.Apply(rec.op)
			if !sameResult(got, rec.res) {
				t.Fatalf("round %d 第 %d 项重放不一致\n输入: %+v\n并发: %s\n重放: %s",
					round, i, rec.op, fmtResult(rec.res), fmtResult(got))
			}
		}
	}
}

// 并发下不可能出现一卷同时借给两人；在借计数与卷状态一致。
func TestConcurrentNoDoubleLend(t *testing.T) {
	s := newSvc(t, testCfg())
	mustOK(t, s.AddVolume(0, "v1", archive.Public), "add v1")
	const n = 32
	for i := 0; i < n; i++ {
		mustOK(t, s.AddBorrower(0, fmt.Sprintf("u%d", i), archive.Public), "add borrower")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]archive.Result, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = s.Borrow(1, fmt.Sprintf("u%d", i), "v1")
		}()
	}
	close(start)
	wg.Wait()
	succeeded := 0
	for _, r := range results {
		if r.Err == nil {
			succeeded++
		} else if r.Err.Code != archive.ErrBorrowed {
			t.Fatalf("非预期错误类别: %v", r.Err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("同一卷并发借阅应恰有一人成功，实际 %d", succeeded)
	}
	snap := s.Snapshot(1)
	loanHolders := map[string]int{}
	for _, vs := range snap.Volumes {
		if vs.Loan != nil {
			loanHolders[vs.Loan.BorrowerID]++
		}
	}
	for _, bs := range snap.Borrowers {
		if bs.ActiveLoans != loanHolders[bs.ID] {
			t.Fatalf("借阅人 %s 在借计数 %d 与卷侧记录 %d 不一致",
				bs.ID, bs.ActiveLoans, loanHolders[bs.ID])
		}
	}
}
