// Package counter 实现一个可增可减的无冲突复制计数器（PN-Counter CRDT）。
//
// 每个副本维护两份非负计数向量：P（增量）与 N（减量）。本地增减只写自己的
// 分量；合并按分量取两侧较大者。任意副本的值为 sum(P) - sum(N)，允许为负。
package counter

import (
	"errors"
	"math/big"
	"sync"
)

// 互不相同、可区分的错误类别。调用方可用 errors.Is 精确判别拒绝原因。
var (
	// ErrInvalidArgument 表示参数本身非法（如副本数为 0、上限 <= 0、nil 参数）。
	ErrInvalidArgument = errors.New("counter: invalid argument")
	// ErrUnknownReplica 表示副本编号不在 [0, ReplicaCount) 范围内。
	ErrUnknownReplica = errors.New("counter: unknown replica id")
	// ErrNonPositiveDelta 表示增量不是正整数（<= 0）。
	ErrNonPositiveDelta = errors.New("counter: non-positive delta")
	// ErrComponentOverflow 表示操作会使某分量超过构造时给定的上限。
	ErrComponentOverflow = errors.New("counter: component overflow")
)

// Counter 是一组互为副本的 PN-Counter。
// 所有方法均可被多个 goroutine 并发调用。
type Counter struct {
	limit uint64
	reps  []*replica
}

type replica struct {
	mu sync.Mutex
	// p 为增量向量、n 为减量向量，下标为副本编号；各分量恒满足 <= limit。
	p []uint64
	n []uint64
}

// New 创建含有 n 个副本的计数器，每个分量不得超过 componentLimit。
func New(n int, componentLimit uint64) (*Counter, error) {
	if n <= 0 || componentLimit == 0 {
		return nil, ErrInvalidArgument
	}
	reps := make([]*replica, n)
	for i := range reps {
		reps[i] = &replica{
			p: make([]uint64, n),
			n: make([]uint64, n),
		}
	}
	return &Counter{limit: componentLimit, reps: reps}, nil
}

// ReplicaCount 返回副本数量。
func (c *Counter) ReplicaCount() int {
	return len(c.reps)
}

// valid 在不加锁的情况下做快速入参校验。错误类别互不相同、可区分。
func (c *Counter) validate(id int, delta uint64) error {
	if id < 0 || id >= len(c.reps) {
		return ErrUnknownReplica
	}
	if delta == 0 {
		return ErrNonPositiveDelta
	}
	return nil
}

// Increment 在副本 replica 的增量向量自身分量上加上正整数 delta。
func (c *Counter) Increment(replica int, delta uint64) error {
	if err := c.validate(replica, delta); err != nil {
		return err
	}
	r := c.reps[replica]
	r.mu.Lock()
	defer r.mu.Unlock()
	// 锁内权威复查，消除校验与加锁之间的并发竞争。
	if delta > c.limit-r.p[replica] {
		return ErrComponentOverflow
	}
	r.p[replica] += delta
	return nil
}

// Decrement 在副本 replica 的减量向量自身分量上加上正整数 delta。
// 计数器值允许变负，不因此拒绝或截断。
func (c *Counter) Decrement(replica int, delta uint64) error {
	if err := c.validate(replica, delta); err != nil {
		return err
	}
	r := c.reps[replica]
	r.mu.Lock()
	defer r.mu.Unlock()
	if delta > c.limit-r.n[replica] {
		return ErrComponentOverflow
	}
	r.n[replica] += delta
	return nil
}

// Merge 把 src 的状态合并进 dst：每个分量取两侧较大者，只修改 dst。
// dst == src 是合法空操作。
func (c *Counter) Merge(dst, src int) error {
	if dst < 0 || dst >= len(c.reps) {
		return ErrUnknownReplica
	}
	if src < 0 || src >= len(c.reps) {
		return ErrUnknownReplica
	}
	if dst == src {
		return nil
	}
	// 始终按副本编号升序获取两把锁：Merge(a,b) 与 Merge(b,a)
	// 并发时加锁顺序一致，不会形成循环等待，因此不会死锁。
	first, second := dst, src
	if first > second {
		first, second = second, first
	}
	c.reps[first].mu.Lock()
	defer c.reps[first].mu.Unlock()
	c.reps[second].mu.Lock()
	defer c.reps[second].mu.Unlock()

	d, s := c.reps[dst], c.reps[src]
	for i := range d.p {
		if s.p[i] > d.p[i] {
			d.p[i] = s.p[i]
		}
		if s.n[i] > d.n[i] {
			d.n[i] = s.n[i]
		}
	}
	return nil
}

// Value 返回副本 replica 的当前值 sum(P) - sum(N)，可能为负。
// 使用 big.Int：多个 uint64 分量之和可能超出 int64 范围。
func (c *Counter) Value(replica int) (*big.Int, error) {
	if replica < 0 || replica >= len(c.reps) {
		return nil, ErrUnknownReplica
	}
	r := c.reps[replica]
	r.mu.Lock()
	defer r.mu.Unlock()
	v := new(big.Int)
	for i := range r.p {
		v.Add(v, new(big.Int).SetUint64(r.p[i]))
		v.Sub(v, new(big.Int).SetUint64(r.n[i]))
	}
	return v, nil
}

// ReplicaState 是单个副本的不可变状态快照。
type ReplicaState struct {
	P []uint64
	N []uint64
}

// Snapshot 返回副本 replica 的深拷贝状态。
func (c *Counter) Snapshot(replica int) (ReplicaState, error) {
	if replica < 0 || replica >= len(c.reps) {
		return ReplicaState{}, ErrUnknownReplica
	}
	r := c.reps[replica]
	r.mu.Lock()
	defer r.mu.Unlock()
	return ReplicaState{
		P: append([]uint64(nil), r.p...),
		N: append([]uint64(nil), r.n...),
	}, nil
}

// SnapshotAll 返回所有副本状态的深拷贝，下标即副本编号。
func (c *Counter) SnapshotAll() []ReplicaState {
	out := make([]ReplicaState, len(c.reps))
	for i := range c.reps {
		s, _ := c.Snapshot(i)
		out[i] = s
	}
	return out
}

// SelfCheck 校验所有副本不变量：分量非负且不超上限。返回发现的首个问题。
func (c *Counter) SelfCheck() error {
	// 按编号升序一次性锁定全部副本，与 Merge 的锁序一致，避免死锁。
	for i := range c.reps {
		c.reps[i].mu.Lock()
	}
	defer func() {
		for i := len(c.reps) - 1; i >= 0; i-- {
			c.reps[i].mu.Unlock()
		}
	}()
	for _, r := range c.reps {
		if len(r.p) != len(c.reps) || len(r.n) != len(c.reps) {
			return ErrInvalidArgument
		}
		for i := range r.p {
			if r.p[i] > c.limit || r.n[i] > c.limit {
				return ErrComponentOverflow
			}
		}
	}
	return nil
}
