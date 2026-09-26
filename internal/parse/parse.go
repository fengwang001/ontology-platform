// Package parse 把 source 的字节消息解析成可聚合记录；坏记录只计数不进组。
package parse

import (
	"bytes"
	"context"
	"strconv"
	"sync/atomic"

	"ontology/internal/source"
	"ontology/internal/stage"
)

// Rec 是解析后的管线消息：屏障、坏记录、正常聚合记录三选一语义。
type Rec struct {
	Off     int
	Key     string
	Val     int64
	Barrier bool
	Bad     bool
}

// Parser 顺序消费 source 消息，坏行（无逗号或 value 非整数）计入 Bad。
type Parser struct {
	bad atomic.Int64
}

func New() *Parser { return &Parser{} }

// Bad 返回截至当前的坏记录数。
func (p *Parser) Bad() int64 { return p.bad.Load() }

// Run 阻塞运行直到 in 关闭排空后返回；out 由本函数关闭。
func (p *Parser) Run(ctx context.Context, in *stage.Queue[source.Msg], out *stage.Queue[Rec]) error {
	defer out.Close()
	for {
		m, ok, err := in.Recv(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		r := Rec{Off: m.Off}
		if m.Barrier {
			r.Barrier = true
		} else {
			key, val, good := decode(m.Data)
			if !good {
				r.Bad = true
				p.bad.Add(1)
			} else {
				r.Key, r.Val = key, val
			}
		}
		if err := out.Send(ctx, r); err != nil {
			return err
		}
	}
}

func decode(line []byte) (string, int64, bool) {
	line = bytes.TrimRight(line, "\n")
	idx := bytes.IndexByte(line, ',')
	if idx < 0 {
		return "", 0, false
	}
	key := string(line[:idx])
	// 缺字段（逗号后为空）视为坏记录；空 key 合法。
	v, err := strconv.ParseInt(string(line[idx+1:]), 10, 64)
	if err != nil {
		return "", 0, false
	}
	return key, v, true
}
