// Package source 提供可注入的脚本数据源。
package source

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Frame 是数据源产出的一帧原始记录。
type Frame struct {
	Seq    int64
	Offset int64
	Raw    []byte
}

// End 返回该帧结束后的字节偏移。
func (f Frame) End() int64 { return f.Offset + int64(len(f.Raw)) }

// Source 是可定位、可恢复的数据源。
type Source interface {
	Read(ctx context.Context) (Frame, error)
	Seek(offset int64) error
}

// EOF 与标准 io.EOF 同义，便于调用方判断正常结束。
var EOF = io.EOF

// Script 是按序号确定性生成帧的脚本数据源（骨架，稍后实现）。
type Script struct {
	// N 是总帧序号数量；Bad 中的序号产出坏行；FailAt>=0 时读该序号返回 ErrFail。
	N, Bad []int64
	// FailAt 为触发错误的序号，-1 表示不触发；Delay 为每帧产出延迟（背压测试设 0）。
	FailAt int64
	Delay  time.Duration
	// KeyLen 控制键名长度，默认 16；seed 固定保证可重放。

	pos int64
}

// ErrFail 是脚本注入的中途错误。
var ErrFail = fmt.Errorf("source: injected failure")

func contains(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// frameAt 确定性生成第 seq 帧的原始字节。
func frameAt(seq int64, bad bool) []byte {
	if bad {
		return []byte(fmt.Sprintf("g%03d:notanumber\n", seq%7))
	}
	return []byte(fmt.Sprintf("g%03d:%d\n", seq%7, seq))
}

// Read 实现 Source。
func (s *Script) Read(ctx context.Context) (Frame, error) {
	if s.Delay > 0 {
		select {
		case <-time.After(s.Delay):
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		}
	}
	if s.FailAt >= 0 && s.pos == s.FailAt {
		return Frame{}, ErrFail
	}
	if s.pos >= int64(len(s.N)) {
		return Frame{}, EOF
	}
	seq := s.N[s.pos]
	raw := frameAt(seq, contains(s.Bad, seq))
	f := Frame{Seq: seq, Offset: s.pos, Raw: raw}
	s.pos++
	return f, nil
}

// Seek 实现 Source。
func (s *Script) Seek(offset int64) error {
	if offset < 0 || offset > int64(len(s.N)) {
		return fmt.Errorf("source: seek %d out of range", offset)
	}
	s.pos = offset
	return nil
}

// RangeN 返回 [0,n) 的序号列表。
func RangeN(n int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i)
	}
	return out
}
