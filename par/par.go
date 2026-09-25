// Package par 把缓冲区按任意字节偏移切成 K 段并行解析再拼接。
package par

import (
	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type evKind int

const (
	evField evKind = iota
	evRecord
)

type event struct {
	kind evKind
	c    cell.Cell
}

type collector struct{ ev []event }

func (c *collector) Field(x cell.Cell) error { c.ev = append(c.ev, event{kind: evField, c: x}); return nil }
func (c *collector) EndRecord() error        { c.ev = append(c.ev, event{kind: evRecord}); return nil }

type runOut struct {
	events []event
	snap   lexer.Snap
	err    *lexer.PosError
	bytes  int64
}

func runOne(seg []byte, base, maxFB int, in lexer.Init) *runOut {
	c := &collector{}
	lx := lexer.New(c, maxFB, base, in)
	err := lx.Feed(seg)
	var pe *lexer.PosError
	if err != nil {
		pe = err.(*lexer.PosError)
	}
	return &runOut{events: c.ev, snap: lx.Snapshot(), err: pe, bytes: lx.BytesProcessed()}
}

// Result 是并行解析结果。
type Result struct {
	Table table.Table
	Err   error
	Bytes int64
}

// Parse 并行解析；K 为 1..8。
func Parse(buf []byte, k int, lim table.Limits) Result {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	n := len(buf)
	if k > n && n > 0 {
		k = n
	}
	bounds := make([]int, k+1)
	for i := 0; i <= k; i++ {
		bounds[i] = n * i / k
	}
	h0 := make([]*runOut, k)
	h1 := make([]*runOut, k)
	cr := make([]*runOut, k)
	done := make(chan int, k)
	for i := 0; i < k; i++ {
		go func(i int) {
			s, e := bounds[i], bounds[i+1]
			h0[i] = runOne(buf[s:e], s, lim.MaxFieldBytes, lexer.Init{})
			h1[i] = runOne(buf[s:e], s, lim.MaxFieldBytes, lexer.Init{InQuote: true, FStart: s})
			// 占位：CR 待定实例在拼接时按需创建（其依赖前段悬挂值）。
			cr[i] = nil
			done <- i
		}(i)
	}
	for i := 0; i < k; i++ {
		<-done
	}
	return stitch(buf, bounds, h0, h1, cr, lim)
}
