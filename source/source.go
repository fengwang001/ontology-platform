// Package source 提供可注入的数据源：可配置速率、中途报错、提前结束。
package source

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrFailed 是数据源在产出中途按配置返回的错误。
var ErrFailed = errors.New("source: injected failure")

// Raw 是 source 产出的一条原始记录。Offset 为该记录之前的记录总数，
// 从 0 起单调递增；坏记录同样占用一个 offset。
type Raw struct {
	Offset int64
	Data   []byte
}

// Source 是数据源接口。正常耗尽返回 io.EOF。
type Source interface {
	Next(ctx context.Context) (Raw, error)
}

// Generator 是内存数据源。
//
//   - Total: 产出总条数；
//   - Rate:  每秒产出条数，<=0 表示全速；
//   - FailAt: 产出恰好 FailAt 条后，下一次 Next 返回 ErrFailed，0 表示不注入；
//   - StartAt: 起始 offset，用于从检查点恢复后重放；
//   - Key/Val: 决定第 i 条（0 基）记录的字节内容，nil 时使用默认实现。
type Generator struct {
	Total   int
	Rate    float64
	FailAt  int64
	StartAt int64
	Key     func(i int64) string
	Val     func(i int64) int

	produced int64
}

// NewGenerator 按总量与速率构造数据源。
func NewGenerator(total int, rate float64) *Generator {
	return &Generator{Total: total, Rate: rate}
}

// Produced 返回已经成功产出的条数。
func (g *Generator) Produced() int64 { return g.produced }

// Next 实现 Source。
func (g *Generator) Next(ctx context.Context) (Raw, error) {
	if g.FailAt > 0 && g.produced >= g.FailAt {
		return Raw{}, ErrFailed
	}
	idx := g.StartAt + g.produced
	if g.produced >= int64(g.Total) {
		return Raw{}, io.EOF
	}
	if g.Rate > 0 {
		interval := time.Duration(float64(time.Second) / g.Rate)
		select {
		case <-ctx.Done():
			return Raw{}, ctx.Err()
		case <-time.After(interval):
		}
	} else {
		select {
		case <-ctx.Done():
			return Raw{}, ctx.Err()
		default:
		}
	}
	data := g.format(idx)
	off := g.StartAt + g.produced
	g.produced++
	return Raw{Offset: off, Data: data}, nil
}

func (g *Generator) format(i int64) []byte {
	key := "g"
	if g.Key != nil {
		key = g.Key(i)
	} else {
		key = keyGroup(i)
	}
	val := i
	if g.Val != nil {
		val = int64(g.Val(i))
	}
	return []byte(key + "=" + itoa(val))
}

func keyGroup(i int64) string {
	switch {
	case i%7 == 0:
		return "a"
	case i%7 == 1:
		return "b"
	default:
		return "c"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
