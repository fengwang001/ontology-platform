// Package single 实现回源单飞：同一个键的并发调用合并为一次真实回源，
// 失败不缓存（每次调用都原样拿到错误，调用方负责回退条目状态），可取消；
// 不同键之间用信号量限制"真实回源"的并发数，但慢键不得阻塞其他键获取席位。
package single

import (
	"context"
	"errors"
	"sync"

	"ontology/entry"
)

// ErrFlightLimit 是三类资源上限错误之一：真实回源并发数已达上限。
var ErrFlightLimit = errors.New("single: in-flight fetch concurrency limit reached")

// Fetcher 由上层提供：真正去后端取一个键的数据。
type Fetcher func(ctx context.Context, key string) (entry.Payload, error)

type call struct {
	done chan struct{}
	val  entry.Payload
	err  error
}

// Group 是单飞协调器。
type Group struct {
	mu     sync.Mutex
	calls  map[string]*call
	maxFly int
	fetchN uint64
}

// New 创建 Group。maxConcurrent<=0 表示不限制真实回源并发数。
func New(maxConcurrent int) *Group {
	return &Group{calls: make(map[string]*call), maxFly: maxConcurrent}
}

// FetchCount 返回 Fetcher 被真实调用的总次数（合并的等待者不计数）。
func (g *Group) FetchCount() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fetchN
}

// InFlight 返回当前正在进行的真实回源键数。
func (g *Group) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}
