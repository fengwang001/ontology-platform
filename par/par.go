// Package par 按任意字节偏移切 K 段并行解析后拼接（DESIGN §4）。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
)

// Options 与 table.Options 同形（镜像，避免 par 反向依赖 table）。
type Options struct {
	MaxFieldBytes, MaxFields, MaxRecords int
	Header                               bool
}

// Result 是并行解析结果。
type Result struct {
	Header    []cell.Cell
	Rows      [][]cell.Cell
	Err       error
	BytesSeen int // worker 状态机处理次数之和（≤2N）
}

type event struct {
	cell   cell.Cell
	recEnd int
}

type sink struct{ ev []event }

func (s *sink) Field(c cell.Cell)       { s.ev = append(s.ev, event{cell: c}) }
func (s *sink) EndRecord(end int) error { s.ev[len(s.ev)-1].recEnd = end; return nil }

type sim struct {
	ev   []event
	err  error
	snap lexer.Snapshot
	seen int
}

type seg struct {
	base    int
	out, in sim
}

func runSim(ch []byte, o lexer.Options, start lexer.Snapshot, closeIt bool) sim {
	s := &sink{}
	c := lexer.Restore(o, s, start)
	e := c.Feed(ch)
	if e == nil && closeIt {
		e = c.Close()
	}
	return sim{ev: s.ev, err: e, snap: c.Snapshot(), seen: c.BytesSeen()}
}

// Parse 切 K 段并行解析。K<1 视为 1。
func Parse(buf []byte, k int, opts Options) Result {
	if k < 1 || len(buf) == 0 {
		k = 1
	} else if k > len(buf) {
		k = len(buf)
	}
	bounds := make([]int, k+1)
	for i := 0; i <= k; i++ {
		bounds[i] = len(buf) * i / k
	}
	lo := lexer.Options{MaxFieldBytes: opts.MaxFieldBytes, MaxFields: opts.MaxFields}
	segs := make([]seg, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, b := bounds[i], bounds[i+1]
			ch := buf[a:b]
			last := i == k-1
			segs[i] = seg{base: a,
				out: runSim(ch, lo, lexer.Snapshot{St: lexer.Start, Off: a}, last),
				in:  runSim(ch, lo, lexer.Snapshot{St: lexer.Quoted, Open: true, Quoted: true, Off: a}, last)}
		}(i)
	}
	wg.Wait()
	return assemble(segs, bounds, opts)
}

type carry struct {
	val   string
	start int
	has   bool
}

func assemble(segs []seg, bounds []int, opts Options) Result {
	var (
		res     Result
		cur     []cell.Cell
		rows    [][]cell.Cell
		width   int
		records int
		first   error
		car     carry // 引号外未闭合字段
		qcar    carry // 引号内：起点与已累积内容
		inQ     bool
	)
	commit := func(rec []cell.Cell, end int) {
		records++
		if first != nil {
			return
		}
		if opts.MaxRecords > 0 && records > opts.MaxRecords {
			first = &lexer.PosError{Err: lexer.ErrTooManyRecords, Offset: end,
				Record: records, Field: len(rec) + 1}
			return
		}
		if width == 0 {
			width = len(rec)
		} else if len(rec) != width {
			first = &lexer.PosError{Err: lexer.ErrColumnCount, Offset: end,
				Record: records, Field: len(rec) + 1}
			return
		}
		rows = append(rows, rec)
	}
	for si := range segs {
		lo, hi := bounds[si], bounds[si+1]
		pick := segs[si].out
		if inQ {
			pick = segs[si].in
		}
		res.BytesSeen += segs[si].out.seen + segs[si].in.seen
		evs := pick.ev
		// OUT 且有 carry：合并进第一个从 lo 起的未引号半字段。
		if !inQ && car.has {
			for i := range evs {
				c0 := &evs[i].cell
				if c0.Start == lo && !c0.Quoted {
					c0.Value = car.val + c0.Value
					c0.Start = car.start
					car.has = false
					break
				}
			}
		}
		// IN 链：累积引号内容，闭合时把全量 Value 与真实起点贴回字段。
		if inQ && qcar.has {
			for i := range evs {
				if evs[i].cell.Quoted {
					evs[i].cell.Value = qcar.val + evs[i].cell.Value
					evs[i].cell.Start = qcar.start
				}
			}
		}
		for _, e := range evs {
			cur = append(cur, e.cell)
			if e.recEnd >= 0 {
				commit(cur, e.recEnd)
				cur = nil
			}
		}
		if first == nil && pick.err != nil {
			first = pick.err
		}
		sn := pick.snap
		nextInQ := sn.St == lexer.Quoted
		switch {
		case nextInQ:
			// 仍在引号内：累积内容；引号起点在“刚打开”段确定。
			if !inQ {
				qcar = carry{start: sn.StartOff}
				qcar.val = string(sn.Val)
			} else {
				qcar.val += string(sn.Val)
			}
			qcar.has = true
		case inQ:
			qcar.has = false // 引号在本段闭合
		}
		if !nextInQ && (sn.St == lexer.Unquoted || sn.St == lexer.CR) && sn.Open {
			car = carry{val: string(sn.Val), start: sn.StartOff, has: true}
		} else if !nextInQ {
			car.has = false
		}
		inQ = nextInQ
		_ = hi
	}
	if first == nil && len(cur) > 0 {
		commit(cur, -1)
	}
	res.Rows, res.Err = rows, first
	if opts.Header && len(rows) > 0 && first == nil {
		res.Header, res.Rows = rows[0], rows[1:]
	}
	return res
}
