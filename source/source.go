// Package source 提供可注入的进程内数据源。
package source

import (
	"context"
	"errors"
	"time"
)

// Item 是 source 产出的一条原始数据。Off 为单调偏移（从 1 起）。
type Item struct {
	Line string
	Off  int64
}

// Source 是可恢复的数据源。Seek 让恢复后的重放从指定偏移之后开始。
type Source interface {
	Seek(afterOff int64)
	Next(ctx context.Context) (Item, bool, error)
}

// BlockedSource 由能统计「产出被阻塞次数」的 source 实现。
type BlockedSource interface {
	Blocked() int64
	NoteBlocked()
}

// ErrInjected 是注入错误的哨兵，调用方可用 errors.Is 判定。
var ErrInjected = errors.New("source: injected error")

// Memory 是内存数据源：可配置速率、中途报错、提前结束。
// Line 为空时按确定性规则生成 "k{key}:{val}"。
type Memory struct {
	N        int           // 总记录数
	Rate     time.Duration // 每条产出间隔，0 表示全速
	FailAt   int           // 1-based 偏移；>0 时在该偏移返回 ErrInjected
	StopAt   int           // >0 时只产出到此偏移即正常结束（提前结束）
	KeyMod   int           // 生成键的取模参数，<=0 视为 1
	Lines    []string      // 可选：自定义记录，长度不足时用生成规则补齐
	blocked  int64
	position int64 // 下一条待产出的偏移
}

// Seek 将重放位置定位到 afterOff 之后。
func (m *Memory) Seek(afterOff int64) {
	if afterOff < 0 {
		afterOff = 0
	}
	m.position = afterOff
}

// NoteBlocked 由下游在因背压而阻塞时调用。
func (m *Memory) NoteBlocked() { m.blocked++ }

// Blocked 返回历史阻塞次数。
func (m *Memory) Blocked() int64 { return m.blocked }

// Next 返回下一条数据；ok=false 表示正常结束。
func (m *Memory) Next(ctx context.Context) (Item, bool, error) {
	if m.KeyMod <= 0 {
		m.KeyMod = 1
	}
	for {
		m.position++
		off := m.position
		if m.FailAt > 0 && off == int64(m.FailAt) {
			return Item{}, false, ErrInjected
		}
		end := m.N
		if m.StopAt > 0 && m.StopAt < end {
			end = m.StopAt
		}
		if off > int64(end) {
			return Item{}, false, nil
		}
		if m.Rate > 0 {
			select {
			case <-ctx.Done():
				return Item{}, false, ctx.Err()
			case <-time.After(m.Rate):
			}
		}
		line := ""
		if int(off)-1 < len(m.Lines) {
			line = m.Lines[off-1]
		} else {
			line = formatLine(off, m.KeyMod)
		}
		return Item{Line: line, Off: off}, true, nil
	}
}

func formatLine(off int64, keyMod int) string {
	key := (off - 1) % int64(keyMod)
	return "k" + itoa(key) + ":" + itoa(off)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
