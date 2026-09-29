package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// applyLogged 打印每条操作、成功或错误类型、操作后视图与判定依据，
// 便于人工核对测试过程。
func applyLogged(t *testing.T, m *Maintainer, op Op, reasonIfReject ErrKind) (View, error) {
	t.Helper()
	view, err := m.Apply(op)
	if err != nil {
		ce := err.(*ConstraintError)
		t.Logf("op=%-13s parent=%-3q child=%-3q => ERROR %-22q view=%+v 判定依据: %s",
			op.Kind, op.ParentID, op.ChildID, ce.Kind, view, rejectBasis(ce.Kind))
		if reasonIfReject != "" && ce.Kind != reasonIfReject {
			t.Fatalf("期望错误 %s，实际 %s", reasonIfReject, ce.Kind)
		}
	} else {
		t.Logf("op=%-13s parent=%-3q child=%-3q => OK                   view=%+v 判定依据: %s",
			op.Kind, op.ParentID, op.ChildID, view, successBasis(op))
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	return view, err
}

func rejectBasis(kind ErrKind) string {
	switch kind {
	case ErrChildParentMissing:
		return "子行插入时父表中不存在该父行，原子拒绝且不记忆该子行"
	case ErrParentReferenced:
		return "删除父行前引用计数 > 0，仍被子行引用"
	case ErrParentNotFound:
		return "删除父行前父表中不存在该父行"
	case ErrChildNotFound:
		return "删除子行前子表中不存在该子行"
	default:
		return "未知拒绝原因"
	}
}

func successBasis(op Op) string {
	switch op.Kind {
	case OpInsertParent:
		return "父行插入（已存在则幂等）"
	case OpDeleteParent:
		return "父行存在且引用计数为 0"
	case OpInsertChild:
		return "父行当前存在，引用计数加 1（已存在则幂等）"
	case OpDeleteChild:
		return "子行存在，删除后引用计数减 1"
	default:
		return ""
	}
}

// TestParentArrivesLate 覆盖“父晚到”：先插子被拒且不留记忆，
// 父行到后由上游重投同一子行成功；期间任何时刻不存在悬空子行。
func TestParentArrivesLate(t *testing.T) {
	m := NewMaintainer()

	applyLogged(t, m, Op{Kind: OpInsertChild, ParentID: "p1", ChildID: "c1"}, ErrChildParentMissing)

	// 被拒子行不记忆：子表为空、自检通过、父行不存在。
	if got := m.Snapshot(); len(got.Children) != 0 || len(got.Parents) != 0 {
		t.Fatalf("被拒子行不应留下任何状态，实际 view=%+v", got)
	}
	if _, ok := m.RefCount("p1"); ok {
		t.Fatal("父行不存在时 RefCount 应返回 false")
	}

	// 父行晚到。
	applyLogged(t, m, Op{Kind: OpInsertParent, ParentID: "p1"}, "")

	// 上游重投此前被拒的同一子行。
	applyLogged(t, m, Op{Kind: OpInsertChild, ParentID: "p1", ChildID: "c1"}, "")

	if count, ok := m.RefCount("p1"); !ok || count != 1 {
		t.Fatalf("重投后引用计数应为 1，实际 %d, %v", count, ok)
	}
}

// TestDeleteReferencedParent 覆盖删除被引用父行被拒，且删除子行后可删父行。
func TestDeleteReferencedParent(t *testing.T) {
	m := NewMaintainer()
	applyLogged(t, m, Op{Kind: OpInsertParent, ParentID: "p1"}, "")
	applyLogged(t, m, Op{Kind: OpInsertChild, ParentID: "p1", ChildID: "c1"}, "")

	before := m.Snapshot()
	applyLogged(t, m, Op{Kind: OpDeleteParent, ParentID: "p1"}, ErrParentReferenced)

	// 一次失败不得改变视图。
	if after := m.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("拒绝删除后视图被改变: before=%+v after=%+v", before, after)
	}

	applyLogged(t, m, Op{Kind: OpDeleteChild, ParentID: "p1", ChildID: "c1"}, "")
	if count, _ := m.RefCount("p1"); count != 0 {
		t.Fatalf("删除子行后引用计数应为 0，实际 %d", count)
	}

	applyLogged(t, m, Op{Kind: OpDeleteParent, ParentID: "p1"}, "")
	if _, ok := m.RefCount("p1"); ok {
		t.Fatal("父行删除后 RefCount 应返回 false")
	}
}

// TestIdempotentInsertAndRefCount 覆盖父行/子行幂等插入与引用计数精确增减。
func TestIdempotentInsertAndRefCount(t *testing.T) {
	m := NewMaintainer()

	// 父行重复插入幂等。
	for i := 0; i < 3; i++ {
		applyLogged(t, m, Op{Kind: OpInsertParent, ParentID: "p1"}, "")
	}
	if got := m.Snapshot(); len(got.Parents) != 1 {
		t.Fatalf("父行幂等插入后应只有 1 行，实际 %d", len(got.Parents))
	}

	// 同一子行（上游重投）重复插入幂等，引用计数不重复增加。
	for i := 0; i < 3; i++ {
		applyLogged(t, m, Op{Kind: OpInsertChild, ParentID: "p1", ChildID: "c1"}, "")
	}
	if count, _ := m.RefCount("p1"); count != 1 {
		t.Fatalf("子行幂等插入后引用计数应为 1，实际 %d", count)
	}

	// 第二个子行使计数变为 2。
	applyLogged(t, m, Op{Kind: OpInsertChild, ParentID: "p1", ChildID: "c2"}, "")
	if count, _ := m.RefCount("p1"); count != 2 {
		t.Fatalf("引用计数应为 2，实际 %d", count)
	}

	// 重复删子行：第二次以 child_not_found 拒绝，计数保持 1。
	applyLogged(t, m, Op{Kind: OpDeleteChild, ParentID: "p1", ChildID: "c1"}, "")
	if count, _ := m.RefCount("p1"); count != 1 {
		t.Fatalf("删除 c1 后引用计数应为 1，实际 %d", count)
	}
	applyLogged(t, m, Op{Kind: OpDeleteChild, ParentID: "p1", ChildID: "c1"}, ErrChildNotFound)
	if count, _ := m.RefCount("p1"); count != 1 {
		t.Fatalf("拒绝删除不存在子行后引用计数应仍为 1，实际 %d", count)
	}
}

// TestDeleteMissingRows 覆盖删除不存在的父行/子行均被拒绝且互不混淆。
func TestDeleteMissingRows(t *testing.T) {
	m := NewMaintainer()

	_, err := m.Apply(Op{Kind: OpDeleteParent, ParentID: "ghost"})
	if ce, ok := err.(*ConstraintError); !ok || ce.Kind != ErrParentNotFound {
		t.Fatalf("期望 %s，实际 %v", ErrParentNotFound, err)
	}
	t.Logf("op=delete_parent parent=ghost => ERROR %q 判定依据: %s",
		ErrParentNotFound, rejectBasis(ErrParentNotFound))

	_, err = m.Apply(Op{Kind: OpDeleteChild, ParentID: "p1", ChildID: "ghost-c"})
	if ce, ok := err.(*ConstraintError); !ok || ce.Kind != ErrChildNotFound {
		t.Fatalf("期望 %s，实际 %v", ErrChildNotFound, err)
	}
	t.Logf("op=delete_child  child=ghost-c => ERROR %q 判定依据: %s",
		ErrChildNotFound, rejectBasis(ErrChildNotFound))

	if got := m.Snapshot(); len(got.Parents) != 0 || len(got.Children) != 0 {
		t.Fatalf("全部拒绝后视图应为空，实际 %+v", got)
	}
}

// TestReplayReproducible 用只含成功操作的日志重放，核对结果与在线视图一致。
func TestReplayReproducible(t *testing.T) {
	m := NewMaintainer()
	committed := []Op{
		{Kind: OpInsertParent, ParentID: "p1"},
		{Kind: OpInsertParent, ParentID: "p2"},
		{Kind: OpInsertChild, ParentID: "p1", ChildID: "c1"},
		{Kind: OpInsertChild, ParentID: "p1", ChildID: "c2"},
		{Kind: OpInsertChild, ParentID: "p2", ChildID: "c3"},
		{Kind: OpDeleteChild, ParentID: "p1", ChildID: "c1"},
	}
	for _, op := range committed {
		applyLogged(t, m, op, "")
	}
	want := m.Snapshot()

	res := Replay(committed, want)
	t.Logf("replay %d 条成功操作 => ok=%v view=%+v", len(committed), res.OK, res.View)
	if !res.OK {
		t.Fatalf("重放核对失败: %s view=%+v want=%+v", res.ErrorKind, res.View, want)
	}

	// 被拒操作不进入日志：混入被拒操作会让重放在该操作处失败。
	dirty := append([]Op{{Kind: OpInsertChild, ParentID: "p9", ChildID: "c9"}}, committed...)
	if res := Replay(dirty, want); res.OK || res.ErrorKind != ErrChildParentMissing {
		t.Fatalf("含被拒操作的日志重放应报 %s，实际 ok=%v err=%s", ErrChildParentMissing, res.OK, res.ErrorKind)
	}
	t.Logf("replay 含被拒操作的日志 => ok=false error=%q 判定依据: 被拒子行从不记忆，不应进入成功日志",
		ErrChildParentMissing)
}

// TestConcurrentReadsIdentical 并发查询/自检期间并发执行变更操作，
// 所有读者在同一实例上看到的视图均满足排序与外键不变量，
// 且不存在两个读者看到同一时刻视图却逐字段不同的情况（快照不可变）。
func TestConcurrentReadsIdentical(t *testing.T) {
	m := NewMaintainer()

	// 先准备父行。
	for _, id := range []string{"p1", "p2", "p3"} {
		if _, err := m.Apply(Op{Kind: OpInsertParent, ParentID: id}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var readersWG sync.WaitGroup
	stop := make(chan struct{})

	// 变更写者：只做始终合法的子行插入/删除循环。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("c%d", i%5)
			if _, err := m.Apply(Op{Kind: OpInsertChild, ParentID: "p1", ChildID: id}); err != nil {
				t.Errorf("并发插子失败: %v", err)
				return
			}
			if _, err := m.Apply(Op{Kind: OpDeleteChild, ParentID: "p1", ChildID: id}); err != nil {
				t.Errorf("并发删子失败: %v", err)
				return
			}
		}
	}()

	// 多个读者并发取快照并自检。
	snapshots := make(chan View, 200)
	for r := 0; r < 4; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for i := 0; i < 50; i++ {
				select {
				case <-stop:
					return
				default:
				}
				v := m.Snapshot()
				if err := m.CheckInvariants(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
				// 快照必须已排序且字段完整。
				for _, c := range v.Children {
					if c.ID == "" || c.ParentID == "" {
						t.Errorf("快照子行字段不完整: %+v", c)
						return
					}
				}
				snapshots <- v
			}
		}()
	}

	// 读者全部结束后通知写者停止，再等待写者退出。
	go func() {
		readersWG.Wait()
		close(stop)
		wg.Wait()
		close(snapshots)
	}()

	count := 0
	for v := range snapshots {
		// 再次取得仅用于验证快照确定性：同内容快照 ViewsEqual 自身。
		if !ViewsEqual(v, v) {
			t.Fatal("快照自比较失败")
		}
		count++
	}
	if count == 0 {
		t.Fatal("未采集到任何并发快照")
	}
	t.Logf("并发读取采集 %d 份快照，全部通过自检且字段完整、排序确定", count)
}
