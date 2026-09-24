// Package pace 是令牌桶驱动的限速 FIFO 队列：保序吐出、追赶判定。依赖 tok。
package pace

import (
	"errors"

	"ontology/tok"
)

// ErrQueueFull：Enqueue 追加后长度会超过 maxQueue，整体拒绝。
var ErrQueueFull = errors.New("pace: enqueue would exceed max queue")

// Queue 单 goroutine 使用。读方法的并发安全由外层 api 的 RWMutex 提供。
type Queue struct {
	b   *tok.Bucket
	q   []int64
	max int
	// scanned 是最近一次 Emit 为决定「吐出多少个」而逐个遍历过的条目数。
	// 个数只由桶内令牌与 len(q) 决定（O(1)），从不逐条扫描，故恒为 0。
	scanned int
}

// New：rate>0、catch>=rate、burst>0、maxQueue>0；初始满桶空队列。
func New(rate, catch, burst int64, maxQueue int) (*Queue, error) {
	if maxQueue <= 0 {
		return nil, tok.ErrInvalidParam
	}
	b, err := tok.New(rate, catch, burst)
	if err != nil {
		return nil, err
	}
	return &Queue{b: b, max: maxQueue}, nil
}

// Advance 推进时钟；补充速率按本次调用开始时刻队列是否非空选择。
func (p *Queue) Advance(t int64) error {
	return p.b.Advance(t, len(p.q) > 0)
}

// Enqueue 整体追加 ids；会超长则一个都不入（失败不留痕）。
func (p *Queue) Enqueue(ids ...int64) error {
	if len(p.q)+len(ids) > p.max {
		return ErrQueueFull
	}
	p.q = append(p.q, ids...)
	return nil
}

// Emit 从队头起每事件扣 1 令牌按序吐出，令牌耗尽或队列空即停。
func (p *Queue) Emit() []int64 {
	p.scanned = 0 // 判定只问桶与 len(q)，不扫描条目。
	k := p.b.Take(len(p.q))
	out := append([]int64(nil), p.q[:k]...)
	p.q = p.q[k:]
	return out
}

func (p *Queue) Pending() int  { return len(p.q) }
func (p *Queue) Tokens() int64 { return p.b.Tokens() }
func (p *Queue) Now() int64    { return p.b.Now() }
