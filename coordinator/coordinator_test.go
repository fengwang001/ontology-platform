package coordinator

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// checkErr 断言 err 为携带指定原因的 RejectError，并打印输入、输出与判定依据。
func checkErr(t *testing.T, op string, err error, want RejectReason) *RejectError {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("%s: got non-reject error %v, want reason %q", op, err, want)
	}
	if re.Reason != want {
		t.Fatalf("%s: got reason %q, want %q (detail: %s)", op, re.Reason, want, re.Detail)
	}
	t.Logf("input=%s output=reject reason=%q detail=%q parents=%v", op, re.Reason, re.Detail, re.Parents)
	return re
}

func mustOK(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", op, err)
	}
	t.Logf("input=%s output=ok", op)
}

// TestSplitChildWaitsForParentDrain 分裂后子分片须待父分片排空才可领取。
func TestSplitChildWaitsForParentDrain(t *testing.T) {
	c := New(100, 10, 4)

	for i, want := range []int64{0, 1} {
		sid, pos, err := c.Append(10)
		mustOK(t, fmt.Sprintf("Append(10)#%d", i), err)
		if sid != 0 || pos != want {
			t.Fatalf("Append(10)#%d = (%d,%d), want (0,%d)", i, sid, pos, want)
		}
		t.Logf("input=Append(10)#%d output=(shard=%d,pos=%d) 依据: 键10路由到开放分片0, 位置从0起递增", i, sid, pos)
	}

	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)
	t.Logf("input=Split(0,50) output=(%d,%d) 依据: 分片0关闭(end=2), 子分片[0,50)与[50,100)位置从0起", l, r)

	_, err = c.Acquire(l, "w1")
	re := checkErr(t, fmt.Sprintf("Acquire(%d,w1)", l), err, ReasonParentsNotDrained)
	if !reflect.DeepEqual(re.Parents, []int{0}) {
		t.Fatalf("undrained parents = %v, want [0]", re.Parents)
	}

	if _, err = c.Acquire(0, "w0"); err != nil {
		t.Fatalf("Acquire(0,w0): %v", err)
	}
	mustOK(t, "Commit(0,w0,1)", c.Commit(0, "w0", 1))

	_, err = c.Acquire(l, "w1")
	checkErr(t, fmt.Sprintf("Acquire(%d,w1) after partial commit", l), err, ReasonParentsNotDrained)
	t.Logf("依据: 父分片0 committed=1 < end=2, 未排空, 子分片不可领")

	mustOK(t, "Commit(0,w0,2)", c.Commit(0, "w0", 2))
	if _, err = c.Acquire(l, "w1"); err != nil {
		t.Fatalf("Acquire(%d,w1) after parent drained: %v", l, err)
	}
	t.Logf("input=Acquire(%d,w1) output=ok 依据: 父分片0 committed==end==2 已排空, 子分片放行", l)
	if _, err = c.Acquire(r, "w2"); err != nil {
		t.Fatalf("Acquire(%d,w2) after parent drained: %v", r, err)
	}
}

// TestMergeChildWaitsForBothParents 合并子分片需两个父分片都排空。
func TestMergeChildWaitsForBothParents(t *testing.T) {
	c := New(100, 10, 4)

	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)

	sid, pos, err := c.Append(10)
	mustOK(t, "Append(10)", err)
	if sid != l || pos != 0 {
		t.Fatalf("Append(10) = (%d,%d), want (%d,0)", sid, pos, l)
	}
	sid, pos, err = c.Append(60)
	mustOK(t, "Append(60)", err)
	if sid != r || pos != 0 {
		t.Fatalf("Append(60) = (%d,%d), want (%d,0)", sid, pos, r)
	}

	m, err := c.Merge(l, r)
	mustOK(t, fmt.Sprintf("Merge(%d,%d)", l, r), err)
	t.Logf("input=Merge(%d,%d) output=%d 依据: 两父分片关闭(end=1,1), 子分片[0,100)位置从0起", l, r, m)

	_, err = c.Acquire(m, "w")
	re := checkErr(t, fmt.Sprintf("Acquire(%d,w)", m), err, ReasonParentsNotDrained)
	if !reflect.DeepEqual(re.Parents, []int{l, r}) {
		t.Fatalf("undrained parents = %v, want [%d %d]", re.Parents, l, r)
	}
	t.Logf("依据: 两个父分片均未排空, 全部列出")

	if _, err = c.Acquire(l, "w1"); err != nil {
		t.Fatalf("Acquire(%d,w1): %v", l, err)
	}
	mustOK(t, fmt.Sprintf("Commit(%d,w1,1)", l), c.Commit(l, "w1", 1))

	_, err = c.Acquire(m, "w")
	re = checkErr(t, fmt.Sprintf("Acquire(%d,w) after left drained", m), err, ReasonParentsNotDrained)
	if !reflect.DeepEqual(re.Parents, []int{r}) {
		t.Fatalf("undrained parents = %v, want [%d]", re.Parents, r)
	}
	t.Logf("依据: 父分片%d已排空, 父分片%d committed=0 < end=1 未排空", l, r)

	if _, err = c.Acquire(r, "w2"); err != nil {
		t.Fatalf("Acquire(%d,w2): %v", r, err)
	}
	mustOK(t, fmt.Sprintf("Commit(%d,w2,1)", r), c.Commit(r, "w2", 1))

	if _, err = c.Acquire(m, "w"); err != nil {
		t.Fatalf("Acquire(%d,w) after both parents drained: %v", m, err)
	}
	t.Logf("input=Acquire(%d,w) output=ok 依据: 两个父分片均已排空, 子分片放行", m)
}

// TestEmptyParentDrainsImmediately 关闭时无记录的父分片立即视为已排空并放行子分片。
func TestEmptyParentDrainsImmediately(t *testing.T) {
	c := New(100, 10, 4)

	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)
	snap, _ := c.Snapshot(0)
	if !snap.Drained() {
		t.Fatalf("shard0 should be drained immediately: %+v", snap)
	}
	t.Logf("依据: 分片0关闭时无记录, end=0=committed, 立即排空")

	if _, err = c.Acquire(l, "w1"); err != nil {
		t.Fatalf("Acquire(%d,w1): %v", l, err)
	}
	t.Logf("input=Acquire(%d,w1) output=ok 依据: 空父分片立即放行", l)

	m, err := c.Merge(l, r)
	mustOK(t, fmt.Sprintf("Merge(%d,%d)", l, r), err)
	if _, err = c.Acquire(m, "w2"); err != nil {
		t.Fatalf("Acquire(%d,w2): %v", m, err)
	}
	t.Logf("input=Acquire(%d,w2) output=ok 依据: 合并的两个空父分片均立即排空", m)
}

// TestLeaseExpiryAndTakeover 租约恰在到期时刻失效，他人接管后旧持有者提交被拒。
func TestLeaseExpiryAndTakeover(t *testing.T) {
	c := New(10, 5, 2)

	if _, _, err := c.Append(3); err != nil {
		t.Fatalf("Append(3): %v", err)
	}
	exp, err := c.Acquire(0, "w1")
	mustOK(t, "Acquire(0,w1)", err)
	if exp != 5 {
		t.Fatalf("expiry = %d, want 5 (clock0 + ttl5)", exp)
	}

	mustOK(t, "AdvanceClock(4)", c.AdvanceClock(4))
	mustOK(t, "Commit(0,w1,1) at clock4", c.Commit(0, "w1", 1))
	t.Logf("依据: clock4 < expiry5, 租约有效")

	mustOK(t, "AdvanceClock(5)", c.AdvanceClock(5))
	checkErr(t, "Commit(0,w1,1) at clock5", c.Commit(0, "w1", 1), ReasonNotHolder)
	if _, err = c.Renew(0, "w1"); true {
		checkErr(t, "Renew(0,w1) at clock5", err, ReasonNotHolder)
	}
	t.Logf("依据: clock5 >= expiry5, 恰在到期时刻失效")

	exp2, err := c.Acquire(0, "w2")
	mustOK(t, "Acquire(0,w2)", err)
	if exp2 != 10 {
		t.Fatalf("expiry = %d, want 10 (clock5 + ttl5)", exp2)
	}
	t.Logf("input=Acquire(0,w2) output=expiry=%d 依据: 旧租约已失效, 他人可领取", exp2)

	checkErr(t, "Commit(0,w1,1) after takeover", c.Commit(0, "w1", 1), ReasonNotHolder)
	mustOK(t, "Commit(0,w2,1)", c.Commit(0, "w2", 1))
}
