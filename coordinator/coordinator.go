// Package coordinator 实现分片谱系与租约消费协调器。
//
// 协调器管理整数键空间 [0,K) 上的数据流分片：分片可分裂与合并，
// 形成谱系树；消费方通过租约独占分片并提交消费进度，保证：
//   - 任一分片任一时刻至多一个有效持有者；
//   - 同一键的记录始终按追加顺序被提交消费；
//   - 追加不丢不重，同一操作序列重放结果完全相同。
package coordinator

import (
	"fmt"
	"sync"
)

// RejectReason 表示操作被拒绝的原因类别。
type RejectReason string

const (
	ReasonShardNotFound     RejectReason = "shard not found"
	ReasonShardClosed       RejectReason = "shard already closed"
	ReasonSplitPointInvalid RejectReason = "split point out of range"
	ReasonNotAdjacent       RejectReason = "shards not adjacent"
	ReasonShardDrained      RejectReason = "shard already drained"
	ReasonParentsNotDrained RejectReason = "parents not drained"
	ReasonLeaseHeld         RejectReason = "shard has active holder"
	ReasonWorkerLimit       RejectReason = "worker lease limit exceeded"
	ReasonNotHolder         RejectReason = "not the valid lease holder"
	ReasonProgressRegress   RejectReason = "commit progress regresses"
	ReasonProgressOverflow  RejectReason = "commit progress exceeds appended"
	ReasonClockRegress      RejectReason = "clock regresses"
	ReasonKeyOutOfRange     RejectReason = "key out of range"
)

// RejectError 描述一次被拒绝的操作，携带判定依据。
type RejectError struct {
	Op      string
	Reason  RejectReason
	Detail  string
	Parents []int // ReasonParentsNotDrained 时列出全部未排空父分片
}

func (e *RejectError) Error() string {
	if len(e.Parents) > 0 {
		return fmt.Sprintf("%s rejected: %s (%s), undrained parents=%v", e.Op, e.Reason, e.Detail, e.Parents)
	}
	return fmt.Sprintf("%s rejected: %s (%s)", e.Op, e.Reason, e.Detail)
}

// Shard 是分片的描述与运行时状态快照。
type Shard struct {
	ID        int
	Lo, Hi    int
	Parents   []int
	Open      bool
	Appended  int64 // 已追加条数
	End       int64 // 结束位置（关闭时的已追加条数；开放时为 -1）
	Committed int64 // 已提交进度
	Holder    string
	Expiry    int64 // 租约到期时刻；clock >= Expiry 即失效
	Leased    bool
}

// Drained 报告分片是否已排空：已关闭且已提交进度等于结束位置。
func (s *Shard) Drained() bool {
	return !s.Open && s.Committed == s.End
}

// Coordinator 是分片谱系与租约消费协调器，所有方法可并发调用。
type Coordinator struct {
	mu        sync.Mutex
	k         int
	ttl       int64
	maxLeases int
	clock     int64
	nextID    int
	shards    map[int]*Shard
	openList  []int // 开放分片 ID，按区间升序排列，覆盖整个键空间
}

// New 创建协调器。k 为键空间大小，ttl 为租约有效时长（时钟单位），
// maxLeasesPerWorker 为同一工作者允许持有的有效租约数上限。
func New(k int, ttl int64, maxLeasesPerWorker int) *Coordinator {
	c := &Coordinator{
		k:         k,
		ttl:       ttl,
		maxLeases: maxLeasesPerWorker,
		shards:    make(map[int]*Shard),
	}
	root := &Shard{ID: 0, Lo: 0, Hi: k, Open: true, End: -1}
	c.shards[0] = root
	c.openList = []int{0}
	c.nextID = 1
	return c
}

// Snapshot 返回分片当前状态的拷贝。
func (c *Coordinator) Snapshot(id int) (Shard, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return Shard{}, false
	}
	cp := *s
	cp.Parents = append([]int(nil), s.Parents...)
	return cp, true
}

// Clock 返回当前时钟。
func (c *Coordinator) Clock() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock
}
