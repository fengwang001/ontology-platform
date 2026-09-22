package single

import (
	"context"

	"ontology/entry"
)

// Do 返回 key 的回源结果。同键并发调用只会让 fn 真实执行一次，
// 全部调用者拿到同一份结果与错误（失败不会被 Group 缓存：没有新调用者时
// 下一次 Do 会重新真实回源）。
//
// 并发席位：真实回源数达到上限时，新键立即得到 ErrFlightLimit，不阻塞、
// 不排队，因此一个慢键不会阻塞其他键。
//
// 取消：调用者的 ctx 取消后，仅该调用者提前返回 ctx.Err()；
// 已合并的回源不受影响，其余等待者继续等同一结果。
func (g *Group) Do(ctx context.Context, key string, fn Fetcher) (entry.Payload, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		return g.wait(ctx, c)
	}
	if g.maxFly > 0 && len(g.calls) >= g.maxFly {
		g.mu.Unlock()
		return entry.Payload{}, ErrFlightLimit
	}
	c := &call{done: make(chan struct{})}
	g.calls[key] = c
	g.fetchN++
	g.mu.Unlock()

	go func() {
		// 回源脱离任何单个调用者的 ctx 运行：等待者共享同一次回源，
		// 某个调用者取消不应杀死其他人正在等的结果。
		c.val, c.err = fn(context.Background(), key)
		close(c.done)

		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
	}()

	return g.wait(ctx, c)
}

func (g *Group) wait(ctx context.Context, c *call) (entry.Payload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		return entry.Payload{}, ctx.Err()
	}
}
