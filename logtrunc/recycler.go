package logtrunc

import (
	"context"
	"time"
)

// StartRecycler 启动周期回收：每隔 interval 将已持久化前缀截断回收，
// 保留最近 retainEntries 条已持久化条目（为 0 表示回收到持久化位点）。
// 返回的函数用于停止回收并等待其退出；ctx 取消也会停止回收。
func (l *Log) StartRecycler(ctx context.Context, interval time.Duration, retainEntries uint64) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := l.RecycleOnce(retainEntries); err != nil {
					l.logf("recycle: 回收失败: %v", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// RecycleOnce 立即执行一次回收：截断到 persisted-retainEntries。
// 返回实际截断到的位点；无可回收前缀时返回当前起始偏移。
func (l *Log) RecycleOnce(retainEntries uint64) (uint64, error) {
	l.mu.RLock()
	start := l.start
	persisted := l.persisted
	l.mu.RUnlock()

	to := persisted
	if retainEntries > 0 && persisted-start > retainEntries {
		to = persisted - retainEntries
	}
	if to <= start {
		l.logf("recycle: 无可回收前缀 start=%d persisted=%d retain=%d", start, persisted, retainEntries)
		return start, nil
	}
	if err := l.Truncate(to); err != nil {
		return start, err
	}
	l.logf("recycle: 回收前缀 [%d,%d) persisted=%d", start, to, persisted)
	return to, nil
}
