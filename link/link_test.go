package link

import (
	"errors"
	"testing"

	"ontology/region"
)

var errBoom = errors.New("boom")

func id(o string, s int64) region.VersionID { return region.VersionID{Origin: o, Seq: s} }

func mkItem(o string, s int64, size int64) Item {
	return Item{ID: id(o, s), Key: "k", Size: size, TS: 1}
}

func TestEnqueueBacklogAndExactCapacity(t *testing.T) {
	l := New(10, 2)
	if !l.Enqueue(mkItem("A", 1, 6)) {
		t.Fatalf("空链路6<=10应入队")
	}
	if l.Enqueue(mkItem("A", 2, 5)) {
		t.Fatalf("6+5>10必须拒绝且状态不变")
	}
	if l.Pending() != 1 || l.Backlog() != 6 || l.Enqueued() != 1 {
		t.Fatalf("被拒入队不得改变队列/积压/计数")
	}
	if !l.Enqueue(mkItem("A", 3, 4)) {
		t.Fatalf("6+4==10 恰等必须通过")
	}
	if l.Backlog() != 10 || l.Pending() != 2 {
		t.Fatalf("积压应为10, got %d/%d", l.Backlog(), l.Pending())
	}
}

func TestDeliverSuccessAppliesAndDequeues(t *testing.T) {
	l := New(100, 2)
	l.Enqueue(mkItem("A", 1, 6))
	l.Enqueue(mkItem("A", 2, 5))
	var applied []region.VersionID
	calls := 0
	l.DeliverN(5,
		func(it Item) (bool, error) { calls++; return true, nil },
		func(it Item) bool { applied = append(applied, it.ID); return false })
	if calls != 2 || len(applied) != 2 || l.Pending() != 0 || l.Backlog() != 0 {
		t.Fatalf("排空失败 calls=%d applied=%d pending=%d", calls, len(applied), l.Pending())
	}
	if l.Enqueued() != l.Delivered()+l.FailedTransfers()+int64(l.Pending()) {
		t.Fatalf("恒等式不成立")
	}
}

func TestDeliverFailureBlockingAndFailoverR2(t *testing.T) {
	l := New(100, 2)
	first := mkItem("A", 1, 6)
	second := mkItem("A", 2, 5)
	l.Enqueue(first)
	l.Enqueue(second)

	// 第一次 Deliver：队首失败1次，<R 阻塞，第二项不动。
	l.DeliverN(10,
		func(it Item) (bool, error) { return false, errBoom },
		func(it Item) bool { t.Fatalf("未确认且 err 非空不应 Apply"); return false })
	if l.Pending() != 2 || l.Backlog() != 11 || l.FailedTransfers() != 0 {
		t.Fatalf("一次失败后应保持阻塞")
	}

	// 第二次失败：达到 R=2，失败转移并在同一次 Deliver 内继续第二项。
	var seenOnSecond []region.VersionID
	l.DeliverN(10,
		func(it Item) (bool, error) {
			if it.ID == first.ID {
				return false, errBoom
			}
			return true, nil
		},
		func(it Item) bool { seenOnSecond = append(seenOnSecond, it.ID); return false })
	if l.Pending() != 0 || l.Backlog() != 0 {
		t.Fatalf("失败转移后第二项应继续处理并清空队列")
	}
	if l.FailedTransfers() != 1 || l.Delivered() != 1 {
		t.Fatalf("失败转移1次、投递1次")
	}
	if len(seenOnSecond) != 1 || seenOnSecond[0] != second.ID {
		t.Fatalf("同一次 Deliver 应继续处理第二项")
	}
	failed := l.Failed()
	if len(failed) != 1 || failed[0].ID != first.ID || failed[0].Attempts != 2 {
		t.Fatalf("失败项应保留两次尝试记录")
	}
}

func TestAckLossAppliedThenError(t *testing.T) {
	l := New(100, 1)
	l.Enqueue(mkItem("A", 1, 6))
	didApply := false
	l.DeliverN(1,
		func(it Item) (bool, error) { return true, errBoom }, // applied 真但 err 非空：确认丢失
		func(it Item) bool { didApply = true; return false })
	if !didApply {
		t.Fatalf("applied==true 时即使 err 非空也必须 Apply")
	}
	if l.Pending() != 0 || l.Backlog() != 0 || l.Delivered() != 0 || l.FailedTransfers() != 1 {
		t.Fatalf("确认丢失且 R=1 应立即失败转移并释放积压")
	}
}

func TestErrNilAppliesAndDequeuesEvenWhenNotApplied(t *testing.T) {
	l := New(100, 3)
	l.Enqueue(mkItem("A", 1, 6))
	applyCalls := 0
	l.DeliverN(1,
		func(it Item) (bool, error) { return false, nil },
		func(it Item) bool { applyCalls++; return true })
	if applyCalls != 1 || l.Pending() != 0 || l.Backlog() != 0 || l.Delivered() != 1 {
		t.Fatalf("err 为空即出队；题面规则 err 为空仍 Apply 一次")
	}
}

func TestRetryOrderingAndBudget(t *testing.T) {
	l := New(10, 2)
	l.Enqueue(mkItem("A", 1, 6))
	// 两次失败 -> 失败列表。
	for i := 0; i < 2; i++ {
		l.DeliverN(1,
			func(it Item) (bool, error) { return false, errBoom },
			func(it Item) bool { return false })
	}
	if l.FailedTransfers() != 1 {
		t.Fatalf("应已失败转移")
	}
	l.Enqueue(mkItem("A", 2, 5)) // 积压5
	if ok, found := l.Retry(id("A", 99)); found {
		t.Fatalf("不在失败列表应 found=false")
	} else if ok {
		t.Fatalf("不存在项不应成功")
	}
	if ok, found := l.Retry(id("A", 1)); ok || !found {
		t.Fatalf("5+6>10 应积压超限且项仍在失败列表")
	}
	// 排空第二项释放积压后可 Retry，回到队尾且计数清零。
	l.DeliverN(1,
		func(it Item) (bool, error) { return true, nil },
		func(it Item) bool { return false })
	if ok, found := l.Retry(id("A", 1)); !ok || !found {
		t.Fatalf("积压归零后 Retry 应成功")
	}
	if l.Pending() != 1 || l.Backlog() != 6 {
		t.Fatalf("Retry 后应重新占用积压")
	}
	// 再失败一次即 <R=2 阻塞，证明 Attempts 已清零。
	l.DeliverN(1,
		func(it Item) (bool, error) { return false, errBoom },
		func(it Item) bool { return false })
	if l.Pending() != 1 || l.FailedTransfers() != 1 {
		t.Fatalf("清零后单次失败不应立即失败转移")
	}
}

func TestIdentityEquation(t *testing.T) {
	l := New(100, 2)
	for s := int64(1); s <= 5; s++ {
		l.Enqueue(mkItem("A", s, 4))
	}
	toggle := false
	l.DeliverN(3, func(it Item) (bool, error) {
		toggle = !toggle
		if toggle {
			return false, errBoom
		}
		return true, nil
	}, func(it Item) bool { return false })
	if l.Enqueued() != l.Delivered()+l.FailedTransfers()+int64(l.Pending()) {
		t.Fatalf("入队恒等式不成立")
	}
}
