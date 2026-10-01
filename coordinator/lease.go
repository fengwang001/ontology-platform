package coordinator

import (
	"fmt"
	"sort"
)

// leaseValidLocked 报告分片当前是否存在有效租约（未到期）。
func (c *Coordinator) leaseValidLocked(s *Shard) bool {
	return s.Leased && c.clock < s.Expiry
}

// workerLeasesLocked 统计工作者当前持有的有效租约数。
func (c *Coordinator) workerLeasesLocked(worker string) int {
	n := 0
	for _, s := range c.shards {
		if s.Leased && s.Holder == worker && c.clock < s.Expiry {
			n++
		}
	}
	return n
}

// Acquire 为工作者 worker 领取分片 id 的租约，返回到期时刻。
// 依次校验：分片不存在、已排空、有父分片未排空（列出全部）、
// 已有有效持有者、工作者超限；拒绝时不改变任何状态。
func (c *Coordinator) Acquire(id int, worker string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return 0, &RejectError{Op: "Acquire", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", id)}
	}
	if s.Drained() {
		return 0, &RejectError{Op: "Acquire", Reason: ReasonShardDrained,
			Detail: fmt.Sprintf("shard %d drained: closed with committed==end==%d", id, s.End)}
	}
	var undrained []int
	for _, p := range s.Parents {
		if !c.shards[p].Drained() {
			undrained = append(undrained, p)
		}
	}
	if len(undrained) > 0 {
		sort.Ints(undrained)
		return 0, &RejectError{Op: "Acquire", Reason: ReasonParentsNotDrained,
			Detail:  fmt.Sprintf("shard %d has %d undrained parent(s)", id, len(undrained)),
			Parents: undrained}
	}
	if c.leaseValidLocked(s) {
		return 0, &RejectError{Op: "Acquire", Reason: ReasonLeaseHeld,
			Detail: fmt.Sprintf("shard %d held by %q until clock %d (now %d)",
				id, s.Holder, s.Expiry, c.clock)}
	}
	if n := c.workerLeasesLocked(worker); n >= c.maxLeases {
		return 0, &RejectError{Op: "Acquire", Reason: ReasonWorkerLimit,
			Detail: fmt.Sprintf("worker %q holds %d valid leases, limit %d", worker, n, c.maxLeases)}
	}
	s.Leased = true
	s.Holder = worker
	s.Expiry = c.clock + c.ttl
	return s.Expiry, nil
}

// checkHolderLocked 校验 worker 是否为分片的有效持有者（含已过期判定）。
func (c *Coordinator) checkHolderLocked(op string, s *Shard, worker string) error {
	switch {
	case !s.Leased:
		return &RejectError{Op: op, Reason: ReasonNotHolder,
			Detail: fmt.Sprintf("shard %d has no lease, worker %q is not a holder", s.ID, worker)}
	case s.Holder != worker:
		return &RejectError{Op: op, Reason: ReasonNotHolder,
			Detail: fmt.Sprintf("shard %d held by %q, not %q", s.ID, s.Holder, worker)}
	case c.clock >= s.Expiry:
		return &RejectError{Op: op, Reason: ReasonNotHolder,
			Detail: fmt.Sprintf("lease of %q on shard %d expired at clock %d (now %d)",
				worker, s.ID, s.Expiry, c.clock)}
	}
	return nil
}

// Renew 续租：自当前时钟起重新有效 ttl。仅有效持有者可续租。
func (c *Coordinator) Renew(id int, worker string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return 0, &RejectError{Op: "Renew", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", id)}
	}
	if err := c.checkHolderLocked("Renew", s, worker); err != nil {
		return 0, err
	}
	s.Expiry = c.clock + c.ttl
	return s.Expiry, nil
}

// Commit 提交消费进度 progress。仅有效持有者可提交；进度只增不减，
// 且不超过已追加条数。依次校验：非有效持有者、进度回退、超过已追加条数。
func (c *Coordinator) Commit(id int, worker string, progress int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return &RejectError{Op: "Commit", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", id)}
	}
	if err := c.checkHolderLocked("Commit", s, worker); err != nil {
		return err
	}
	if progress < s.Committed {
		return &RejectError{Op: "Commit", Reason: ReasonProgressRegress,
			Detail: fmt.Sprintf("progress %d < committed %d on shard %d", progress, s.Committed, id)}
	}
	if progress > s.Appended {
		return &RejectError{Op: "Commit", Reason: ReasonProgressOverflow,
			Detail: fmt.Sprintf("progress %d > appended %d on shard %d", progress, s.Appended, id)}
	}
	s.Committed = progress
	return nil
}
