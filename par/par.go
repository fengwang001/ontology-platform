// Package par 把字节缓冲在任意偏移切成 K 段并行解析，结果与单线程一致。
//
// 第一遍探针顺序词法（无 sink，仅抓切点快照，N 次）确定每段真实入口；
// 第二遍 K 个 worker 从各自快照恢复并行解析（合计 N 次），总计 ≤ 2N。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

const (
	tBegin = iota
	tFrag
	tCell
	tEndRec
)

type tk struct {
	kind        int
	quoted      bool
	start, off  int
	b           byte
	c           cell.Cell
}

type result struct {
	toks []tk
	err  *lexer.Error
	n    int
}

type recorder struct{ r *result }

func (x *recorder) BeginField(q bool, s int) {
	x.r.toks = append(x.r.toks, tk{kind: tBegin, quoted: q, start: s})
}
func (x *recorder) Fragment(b byte, off int) {
	x.r.toks = append(x.r.toks, tk{kind: tFrag, b: b, off: off})
}
func (x *recorder) Cell(c cell.Cell, _, _ int) {
	x.r.toks = append(x.r.toks, tk{kind: tCell, c: c})
}
func (x *recorder) EndRecord(_ int) { x.r.toks = append(x.r.toks, tk{kind: tEndRec}) }
func (x *recorder) AllowBytes(int) error { return nil }

type noopSink struct{}

func (noopSink) Cell(cell.Cell, int, int) {}
func (noopSink) EndRecord(int)            {}

// Parse 把 buf 切成 K 段并行解析。
func Parse(buf []byte, k int, lim table.Limits) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	if k > len(buf) && len(buf) > 0 {
		k = len(buf)
	}
	bounds := make([]int, k)
	for i := 1; i <= k; i++ {
		bounds[i-1] = len(buf) * i / k
	}
	return ParseAt(buf, bounds, lim)
}

var lastN int

// LastProcessed 返回最近一次并行解析的字节处理次数（≤ 2N+常数）。
func LastProcessed() int { return lastN }

// ParseAt 在显式切点 bounds（段末偏移，最后一个须为 len(buf)）并行解析。
func ParseAt(buf []byte, bounds []int, lim table.Limits) (*table.Table, error) {
	type segment struct {
		start, end int
		sn         lexer.Snapshot
	}
	segs := make([]segment, len(bounds))
	prev := 0
	for i, e := range bounds {
		segs[i] = segment{start: prev, end: e}
		prev = e
	}
	// 探针：顺序跑一遍（sink 丢弃事件），在切点抓快照；遇错即停。
	probe := lexer.New(noopSink{})
	pos := 0
	for i := range segs {
		segs[i].sn = probe.Snapshot()
		probe.Feed(buf[pos:segs[i].end])
		pos = segs[i].end
	}
	probe.Close()
	rs := make([]*result, len(segs))
	var wg sync.WaitGroup
	for i := range segs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := &result{}
			lx := lexer.Restore(segs[i].sn, &recorder{r})
			err := lx.Feed(buf[segs[i].start:segs[i].end])
			if err == nil && i == len(segs)-1 {
				err = lx.Close()
			}
			if e, ok := err.(*lexer.Error); ok {
				r.err = e
			}
			r.n = lx.Processed()
			rs[i] = r
		}(i)
	}
	wg.Wait()
	total := probe.Processed()
	for _, r := range rs {
		total += r.n
	}
	lastN = total
	return stitch(rs, lim)
}

func stitch(rs []*result, lim table.Limits) (*table.Table, error) {
	t := &table.Table{Limits: lim}
	recN, fldN, flen := 1, 1, 0
	var val []byte
	var q bool
	var st int
	finish := func(end int) {
		t.Cell(cell.Cell{Value: string(val), Quoted: q, Start: st, End: end}, recN, fldN)
		fldN++
		flen, val = 0, val[:0]
	}
	for _, r := range rs {
		for _, x := range r.toks {
			switch x.kind {
			case tBegin:
				q, st, flen, val = x.quoted, x.start, 0, val[:0]
			case tFrag:
				flen++
				if lim.MaxFieldBytes > 0 && flen > lim.MaxFieldBytes {
					return t, &table.Error{Err: lexer.ErrFieldTooLarge, Offset: x.off, Record: recN, Field: fldN}
				}
				val = append(val, x.b)
			case tCell:
				finish(x.c.End)
			case tEndRec:
				t.EndRecord(recN)
				recN++
				fldN = 1
			}
			if te := t.Terminal(); te != nil {
				return t, te
			}
		}
		if r.err != nil {
			return t, &table.Error{Err: r.err.Err, Offset: r.err.Offset, Record: recN, Field: fldN}
		}
	}
	return t, nil
}
