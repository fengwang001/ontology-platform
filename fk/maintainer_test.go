package fk

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logStep 打印操作、成功或错误类型、当前视图与判定依据。
func logStep(t *testing.T, basis string, op Operation, err error, m *Maintainer) {
	t.Helper()
	result := "成功"
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			result = fmt.Sprintf("拒绝(%s)", rej.Reason)
		} else {
			result = fmt.Sprintf("错误(%T)", err)
		}
	}
	t.Logf("op=%s parent=%q child=%q => %s | 判定依据: %s", op.Kind, op.ParentID, op.ChildID, result, basis)
	t.Logf("  父表视图: %+v", m.ParentView())
	t.Logf("  子表视图: %+v", m.ChildView())
}

func applyAndLog(t *testing.T, m *Maintainer, basis string, op Operation) error {
	t.Helper()
	err := m.Apply(op)
	logStep(t, basis, op, err, m)
	return err
}

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("期望 *RejectError, 实际 %T: %v", err, err)
	}
	return rej.Reason
}

// TestParentLateArrival 父晚到场景：子行先到被拒且不记忆，
// 父行到达后上游重投子行成功，引用计数随之变化。
func TestParentLateArrival(t *testing.T) {
	m := New()

	err := applyAndLog(t, m, "父行 p1 当前不存在, 子行插入应拒绝且不记忆",
		Operation{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1", Fields: map[string]string{"v": "1"}})
	if got := reasonOf(t, err); got != ReasonParentMissing {
		t.Fatalf("期望 %s, 实际 %s", ReasonParentMissing, got)
	}
	if view := m.ChildView(); len(view) != 0 {
		t.Fatalf("被拒子行不应被记忆, 子表视图=%+v", view)
	}

	err = applyAndLog(t, m, "删除从未插入成功的子行 c1 应拒绝",
		Operation{Kind: OpDeleteChild, ChildID: "c1"})
	if got := reasonOf(t, err); got != ReasonChildNotFound {
		t.Fatalf("期望 %s, 实际 %s", ReasonChildNotFound, got)
	}

	if err := applyAndLog(t, m, "父行 p1 晚到, 插入应成功",
		Operation{Kind: OpInsertParent, ParentID: "p1", Fields: map[string]string{"name": "P"}}); err != nil {
		t.Fatalf("父行插入失败: %v", err)
	}

	if err := applyAndLog(t, m, "父行已存在, 上游重投子行 c1 应成功",
		Operation{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1", Fields: map[string]string{"v": "1"}}); err != nil {
		t.Fatalf("重投子行失败: %v", err)
	}
	if n := m.RefCount("p1"); n != 1 {
		t.Fatalf("引用计数应为 1, 实际 %d", n)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestDeleteReferencedParent 删除仍被引用的父行必须整体拒绝且状态不变。
func TestDeleteReferencedParent(t *testing.T) {
	m := New()
	_ = applyAndLog(t, m, "准备: 插入父行 p1", Operation{Kind: OpInsertParent, ParentID: "p1"})
	_ = applyAndLog(t, m, "准备: 插入子行 c1->p1", Operation{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1"})
	_ = applyAndLog(t, m, "准备: 插入子行 c2->p1", Operation{Kind: OpInsertChild, ChildID: "c2", ParentID: "p1"})

	beforeP, beforeC := m.ParentView(), m.ChildView()

	err := applyAndLog(t, m, "p1 引用计数=2>0, 删除应整体拒绝",
		Operation{Kind: OpDeleteParent, ParentID: "p1"})
	if got := reasonOf(t, err); got != ReasonParentReferenced {
		t.Fatalf("期望 %s, 实际 %s", ReasonParentReferenced, got)
	}
	if afterP, afterC := m.ParentView(), m.ChildView(); !reflect.DeepEqual(beforeP, afterP) || !reflect.DeepEqual(beforeC, afterC) {
		t.Fatalf("被拒操作改变了状态: 父 %+v->%+v 子 %+v->%+v", beforeP, afterP, beforeC, afterC)
	}

	_ = applyAndLog(t, m, "删除子行 c1, p1 引用计数 2->1", Operation{Kind: OpDeleteChild, ChildID: "c1"})
	_ = applyAndLog(t, m, "删除子行 c2, p1 引用计数 1->0", Operation{Kind: OpDeleteChild, ChildID: "c2"})
	if n := m.RefCount("p1"); n != 0 {
		t.Fatalf("引用计数应为 0, 实际 %d", n)
	}

	if err := applyAndLog(t, m, "p1 引用计数=0, 删除应成功",
		Operation{Kind: OpDeleteParent, ParentID: "p1"}); err != nil {
		t.Fatalf("删除未被引用父行失败: %v", err)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestIdempotentParentInsert 父行插入幂等：重复插入不改变视图与引用计数。
func TestIdempotentParentInsert(t *testing.T) {
	m := New()
	if err := applyAndLog(t, m, "首次插入 p1 应成功",
		Operation{Kind: OpInsertParent, ParentID: "p1", Fields: map[string]string{"name": "A"}}); err != nil {
		t.Fatalf("首次插入失败: %v", err)
	}
	_ = applyAndLog(t, m, "准备: 子行 c1->p1", Operation{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1"})

	beforeP, beforeC := m.ParentView(), m.ChildView()

	if err := applyAndLog(t, m, "p1 已存在, 重复插入应幂等成功且不改写字段",
		Operation{Kind: OpInsertParent, ParentID: "p1", Fields: map[string]string{"name": "B"}}); err != nil {
		t.Fatalf("幂等插入失败: %v", err)
	}
	afterP, afterC := m.ParentView(), m.ChildView()
	if !reflect.DeepEqual(beforeP, afterP) || !reflect.DeepEqual(beforeC, afterC) {
		t.Fatalf("幂等插入改变了视图: 父 %+v->%+v 子 %+v->%+v", beforeP, afterP, beforeC, afterC)
	}
	if got := afterP[0].Fields["name"]; got != "A" {
		t.Fatalf("幂等插入不应覆盖既有字段, name=%q", got)
	}
	if n := m.RefCount("p1"); n != 1 {
		t.Fatalf("引用计数应保持 1, 实际 %d", n)
	}
}

// TestDeleteNonExistent 删除不存在的父行 / 子行必须拒绝且原因可区分。
func TestDeleteNonExistent(t *testing.T) {
	m := New()
	beforeP, beforeC := m.ParentView(), m.ChildView()

	err := applyAndLog(t, m, "pX 不存在, 删除父行应拒绝",
		Operation{Kind: OpDeleteParent, ParentID: "pX"})
	if got := reasonOf(t, err); got != ReasonParentNotFound {
		t.Fatalf("期望 %s, 实际 %s", ReasonParentNotFound, got)
	}

	err = applyAndLog(t, m, "cX 不存在, 删除子行应拒绝",
		Operation{Kind: OpDeleteChild, ChildID: "cX"})
	if got := reasonOf(t, err); got != ReasonChildNotFound {
		t.Fatalf("期望 %s, 实际 %s", ReasonChildNotFound, got)
	}

	if afterP, afterC := m.ParentView(), m.ChildView(); !reflect.DeepEqual(beforeP, afterP) || !reflect.DeepEqual(beforeC, afterC) {
		t.Fatalf("被拒操作改变了状态")
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestConcurrentViewsIdentical 并发读取同一实例的视图必须逐字段相同，
// 且任意时刻自检通过。
func TestConcurrentViewsIdentical(t *testing.T) {
	m := New()
	for i := 0; i < 8; i++ {
		pid := fmt.Sprintf("p%d", i)
		if err := m.InsertParent(pid, map[string]string{"idx": fmt.Sprint(i)}); err != nil {
			t.Fatalf("插入父行失败: %v", err)
		}
		for j := 0; j < 4; j++ {
			cid := fmt.Sprintf("c%d-%d", i, j)
			if err := m.InsertChild(cid, pid, map[string]string{"v": fmt.Sprint(j)}); err != nil {
				t.Fatalf("插入子行失败: %v", err)
			}
		}
	}

	const readers = 16
	var wg sync.WaitGroup
	errCh := make(chan string, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				pv1, cv1 := m.ParentView(), m.ChildView()
				pv2, cv2 := m.ParentView(), m.ChildView()
				if !reflect.DeepEqual(pv1, pv2) || !reflect.DeepEqual(cv1, cv2) {
					errCh <- "同一实例两次读取的视图不一致"
					return
				}
				if len(pv1) != 8 || len(cv1) != 32 {
					errCh <- fmt.Sprintf("视图行数异常: 父=%d 子=%d", len(pv1), len(cv1))
					return
				}
				for _, row := range pv1 {
					if row.RefCount != 4 {
						errCh <- fmt.Sprintf("父行 %s 引用计数=%d, 期望 4", row.ID, row.RefCount)
						return
					}
				}
				if err := m.SelfCheck(); err != nil {
					errCh <- err.Error()
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	t.Logf("并发读取 %d 个 goroutine x 50 轮, 视图逐字段一致, 自检全部通过", readers)
}

// TestReplayDeterminism 重放成功操作序列可复现相同视图。
func TestReplayDeterminism(t *testing.T) {
	stream := []Operation{
		{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1"}, // 父晚到, 拒绝
		{Kind: OpInsertParent, ParentID: "p1", Fields: map[string]string{"n": "A"}},
		{Kind: OpInsertParent, ParentID: "p1"},               // 幂等
		{Kind: OpInsertChild, ChildID: "c1", ParentID: "p1"}, // 重投, 成功
		{Kind: OpInsertChild, ChildID: "c2", ParentID: "p1"},
		{Kind: OpDeleteParent, ParentID: "p1"}, // 被引用, 拒绝
		{Kind: OpDeleteParent, ParentID: "pX"}, // 不存在, 拒绝
		{Kind: OpDeleteChild, ChildID: "cX"},   // 不存在, 拒绝
		{Kind: OpDeleteChild, ChildID: "c1"},
		{Kind: OpDeleteChild, ChildID: "c2"},
		{Kind: OpDeleteParent, ParentID: "p1"}, // 计数归零, 成功
	}

	m := New()
	var succeeded []Operation
	for i, op := range stream {
		err := m.Apply(op)
		logStep(t, fmt.Sprintf("变更流第 %d 条", i), op, err, m)
		if err == nil {
			succeeded = append(succeeded, op)
		}
		if err := m.SelfCheck(); err != nil {
			t.Fatalf("第 %d 条后自检失败: %v", i, err)
		}
	}

	replay := New()
	for _, op := range succeeded {
		if err := replay.Apply(op); err != nil {
			t.Fatalf("重放成功操作失败: %+v: %v", op, err)
		}
	}
	if pv, rv := m.ParentView(), replay.ParentView(); !reflect.DeepEqual(pv, rv) {
		t.Fatalf("重放后父表视图不一致: %+v vs %+v", pv, rv)
	}
	if cv, rv := m.ChildView(), replay.ChildView(); !reflect.DeepEqual(cv, rv) {
		t.Fatalf("重放后子表视图不一致: %+v vs %+v", cv, rv)
	}
	t.Logf("重放 %d 条成功操作后视图与原实例逐字段一致", len(succeeded))
}
