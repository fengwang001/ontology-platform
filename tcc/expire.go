package tcc

import (
	"container/heap"
	"strconv"
)

// Avail 只读返回可用额；按 now 虚拟到期计算，不落实任何状态变更。
func (t *TCC) Avail(acct string, now int64) (int64, error) {
	if acct == "" {
		return 0, t.fail("Avail", acct, ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkClock(now); err != nil {
		return 0, t.fail("Avail", acct, err)
	}
	popped, converted := t.applyExpired(now)
	// applyExpired 已在账本上虚拟解冻，lg.Avail 即虚拟到期后的可用额。
	_ = converted
	avail := t.lg.Avail(acct)
	t.rollbackExpired(popped, converted)
	t.clock = now
	t.traceN("Avail", acct, avail)
	return avail, nil
}

// Len 返回当前分支记录数。
func (t *TCC) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.recs)
}

// Get 返回分支记录副本；不存在返回 ok=false。
func (t *TCC) Get(xid, br string) (Branch, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if b, ok := t.recs[key(xid, br)]; ok {
		return *b, true
	}
	return Branch{}, false
}

// ExpiryPeeks 返回最近一次到期扫描考察的堆项数（测试用）。
func (t *TCC) ExpiryPeeks() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expiryPeeks
}

func (t *TCC) checkClock(now int64) error {
	if now < minNow || now > maxNow {
		return ErrInvalid
	}
	if now < t.clock {
		return ErrClock
	}
	return nil
}

// applyExpired 试应用到期：解冻并转 Cancelled(Expired)。
// 一次扫描考察堆项数 ≤ 到期数 + 1（到期数为 0 时只看堆顶一次）。
// 操作最终被拒绝时须以 rollbackExpired 原样撤销，保证“拒绝不落实到期”。
func (t *TCC) applyExpired(now int64) (popped []*heapEntry, converted []*Branch) {
	t.expiryPeeks = 0
	for t.hp.Len() > 0 {
		root := t.hp[0]
		t.expiryPeeks++
		if root.deadline > now {
			break
		}
		heap.Pop(&t.hp)
		popped = append(popped, root)
		b := t.recs[root.key]
		if b != nil && b.State == StateTried {
			t.lg.Unfreeze(b.Acct, b.Amount)
			b.State = StateCancelled
			b.Reason = Expired
			delete(t.refs, root.key)
			converted = append(converted, b)
		}
	}
	return popped, converted
}

// rollbackExpired 撤销 applyExpired：重新冻结、恢复 Tried、按原堆序还原。
func (t *TCC) rollbackExpired(popped []*heapEntry, converted []*Branch) {
	for _, b := range converted {
		t.lg.Freeze(b.Acct, b.Amount)
		b.State = StateTried
		b.Reason = ""
	}
	for i := len(popped) - 1; i >= 0; i-- {
		e := popped[i]
		heap.Push(&t.hp, e)
		t.refs[e.key] = e
	}
}

func (t *TCC) commit(op, k string, now int64) {
	t.clock = now
	if t.log != nil {
		t.log.Printf("%s key=%q -> ok", op, k)
	}
}

func (t *TCC) fail(op, k string, err error) error {
	if t.log != nil {
		t.log.Printf("%s key=%q -> %v", op, k, err)
	}
	return err
}

func (t *TCC) traceN(op, k string, n int64) {
	if t.log != nil {
		t.log.Printf("%s key=%q -> %d", op, k, n)
	}
}

func nonEmpty(ss ...string) bool {
	for _, s := range ss {
		if s == "" {
			return false
		}
	}
	return true
}

// key 用长度前缀编码，避免不同 (xid,br) 拼接冲突。
func key(xid, br string) string {
	return strconv.Itoa(len(xid)) + ":" + xid + "/" + br
}
