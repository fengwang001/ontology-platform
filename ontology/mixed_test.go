package ontology

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestMixedBatchConflictDoesNotBlockLaterEvents(t *testing.T) {
	s := NewStore(10)
	// 同一批内：
	//  seq1 插入 k1（应用）
	//  seq2 对不存在的 kX 更新（冲突，跳过）
	//  seq3 更新 k1，前像正确（应用）
	//  seq4 再次更新 k1，但前像用旧值，与 seq3 后的当前行不符（冲突）
	//  seq5 插入 k2（应用）
	res, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"v": "1"}},
		{Seq: 2, Key: "kX", Op: OpUpdate, Before: Row{"v": "0"}, After: Row{"v": "9"}},
		{Seq: 3, Key: "k1", Op: OpUpdate, Before: Row{"v": "1"}, After: Row{"v": "2"}},
		{Seq: 4, Key: "k1", Op: OpUpdate, Before: Row{"v": "1"}, After: Row{"v": "3"}},
		{Seq: 5, Key: "k2", Op: OpInsert, After: Row{"v": "1"}},
	})
	if err != nil {
		t.Fatalf("混合批不应返回错误: %v", err)
	}
	if res.Applied != 3 {
		t.Fatalf("Applied = %d, want 3", res.Applied)
	}
	if len(res.Conflicts) != 2 {
		t.Fatalf("conflicts = %d, want 2", len(res.Conflicts))
	}
	if res.Conflicts[0].Kind != ConflictRowMissing || res.Conflicts[1].Kind != ConflictBeforeMismatch {
		t.Fatalf("冲突分类或顺序错误: %+v", res.Conflicts)
	}
	snap := s.Snapshot()
	if r, _ := snap.Get("k1"); r["v"] != "2" {
		t.Fatalf("k1 = %v, want v=2", r)
	}
	if _, ok := snap.Get("k2"); !ok {
		t.Fatal("k2 应已插入")
	}
	if _, ok := snap.Get("kX"); ok {
		t.Fatal("kX 不应因冲突事件产生行")
	}
	if res.LastSeq != 5 || s.LastSeq() != 5 {
		t.Fatalf("LastSeq = %d/%d, want 5", res.LastSeq, s.LastSeq())
	}
}

func TestDeleteFreesRowQuota(t *testing.T) {
	s := NewStore(1)
	_, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"v": "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 上限 1 行：同批先删后插，删除释放名额，插入不再超限
	res, err := s.Apply([]Event{
		{Seq: 2, Key: "k1", Op: OpDelete, Before: Row{"v": "1"}},
		{Seq: 3, Key: "k2", Op: OpInsert, After: Row{"v": "2"}},
	})
	if err != nil {
		t.Fatalf("先删后插不应超限: %v", err)
	}
	if res.Applied != 2 {
		t.Fatalf("Applied = %d, want 2", res.Applied)
	}
	if s.Snapshot().Len() != 1 {
		t.Fatalf("rows = %d, want 1", s.Snapshot().Len())
	}
	if r, ok := s.Snapshot().Get("k2"); !ok || r["v"] != "2" {
		t.Fatalf("k2 = %v ok=%v", r, ok)
	}
}

// replay 对一个全新 store 重放事件序列，返回便于比较的确定性输出。
func replay(batches [][]Event, maxRows int) string {
	s := NewStore(maxRows)
	out := ""
	for bi, b := range batches {
		prevConflicts := len(s.Conflicts())
		res, err := s.Apply(b)
		if err != nil {
			var re *RejectError
			if AsReject(err, &re) {
				out += fmt.Sprintf("batch%d:REJECT(%s,%d)\n", bi, re.Reason, re.Index)
			} else {
				out += fmt.Sprintf("batch%d:ERR(%v)\n", bi, err)
			}
			continue
		}
		out += fmt.Sprintf("batch%d:applied=%d conflicts=%d last=%d\n",
			bi, res.Applied, len(res.Conflicts), res.LastSeq)
		snap := s.Snapshot()
		keys := append([]string(nil), snap.Keys()...)
		sort.Strings(keys)
		for _, k := range keys {
			r, _ := snap.Get(k)
			out += fmt.Sprintf("  row %s=%v\n", k, r)
		}
		for _, c := range s.Conflicts()[prevConflicts:] {
			out += fmt.Sprintf("  conflict seq=%d kind=%s\n", c.Seq, c.Kind)
		}
	}
	return out
}

func AsReject(err error, target **RejectError) bool {
	re, ok := err.(*RejectError)
	if !ok {
		return false
	}
	*target = re
	return true
}

func TestDeterministicReplay(t *testing.T) {
	batches := [][]Event{
		{
			{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"v": "1"}},
			{Seq: 2, Key: "k2", Op: OpInsert, After: Row{"v": "1"}},
		},
		{
			{Seq: 3, Key: "k1", Op: OpUpdate, Before: Row{"v": "9"}, After: Row{"v": "2"}}, // 冲突
			{Seq: 4, Key: "ghost", Op: OpDelete, Before: Row{"v": "1"}},                    // 冲突
			{Seq: 5, Key: "k1", Op: OpUpdate, Before: Row{"v": "1"}, After: Row{"v": "2"}}, // 应用
			{Seq: 6, Key: "k3", Op: OpInsert, After: Row{"v": "1"}},                        // 超限
		},
		{
			// 第二批被整体回滚后序号从 3 重新提交：k1 前像为回滚后的 v=1，k2 删除
			{Seq: 3, Key: "k1", Op: OpUpdate, Before: Row{"v": "1"}, After: Row{"v": "3"}},
			{Seq: 4, Key: "k2", Op: OpDelete, Before: Row{"v": "1"}},
		},
	}

	first := replay(batches, 2)
	second := replay(batches, 2)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放不确定:\n%s\n!=\n%s", first, second)
	}

	// 期望的关键状态：超限批被整体回滚，seq 停在 2；末批重放 seq3/4 后 k1.v=3、k2 被删。
	s := NewStore(2)
	for _, b := range batches {
		_, _ = s.Apply(b)
	}
	if s.LastSeq() != 4 {
		t.Fatalf("LastSeq = %d, want 4", s.LastSeq())
	}
	if r, _ := s.Snapshot().Get("k1"); r["v"] != "3" {
		t.Fatalf("k1 = %v, want v=3", r)
	}
	if _, ok := s.Snapshot().Get("k2"); ok {
		t.Fatal("k2 应已删除")
	}
	// 超限批的两条冲突也必须随拒绝一起回滚
	if n := len(s.Conflicts()); n != 0 {
		t.Fatalf("被拒绝批的冲突不得落地, conflicts=%d: %+v", n, s.Conflicts())
	}

	if t.Failed() {
		t.Logf("replay output:\n%s", first)
	}
}
