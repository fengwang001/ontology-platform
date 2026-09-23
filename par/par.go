// Package par 把大缓冲区按任意字节偏移切成 K 段并行解析后拼接。
package par

import (
	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type ev struct {
	c     cell.Cell
	isRec bool
}

type collector struct{ evs []ev }

func (c *collector) Cell(x cell.Cell, _ int) { c.evs = append(c.evs, ev{c: x}) }
func (c *collector) EndRecord(_ int)         { c.evs = append(c.evs, ev{isRec: true}) }

type run struct {
	col    *collector
	mode   lexer.Mode
	pref   string
	flen   int64
	fstart int64
	fatal  *lexer.Error
	bytes  int64
}

func execute(buf []byte, base, end int64, m lexer.Mode, lim cell.Limits, prefix string, flen, fstart int64, doClose bool) *run {
	col := &collector{}
	l := lexer.NewAt(m, col, lim, base, prefix, flen, fstart)
	err := l.Feed(buf[base:end])
	if err == nil && doClose {
		err = l.Close()
	}
	_ = err
	r := &run{col: col, fatal: l.Fatal(), bytes: l.BytesSeen()}
	r.mode, r.pref, r.flen, r.fstart = l.State()
	return r
}

type seg struct {
	base, end int64
	a, b      *run // MStart / MQuoted 两假设（未 Close）
	ca, cb    *run // 同两假设，但在段尾执行了 Close（仅最后一段使用）
}

// Result 为并行解析结果。
type Result struct {
	Table     *table.Table
	Err       error
	BytesSeen int64
}

// Parse 切 K 段并发解析 buf，结果（含偏移、错误坐标）与 table.Parse 完全一致。
func Parse(buf []byte, k int, lim cell.Limits) Result {
	n := int64(len(buf))
	if k < 1 {
		k = 1
	}
	if n > 0 && int64(k) > n {
		k = int(n)
	}
	segs := make([]*seg, k)
	for i := range segs {
		segs[i] = &seg{base: n * int64(i) / int64(k), end: n * int64(i+1) / int64(k)}
	}

	// 阶段一：每段按两个起点假设并发执行；最后一段额外各做一次带 Close 的收尾。
	done := make(chan *seg, k)
	sem := make(chan struct{}, k)
	for i, sg0 := range segs {
		sg, idx := sg0, i
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- sg }()
			if sg.end <= sg.base {
				empty := &run{col: &collector{}, mode: lexer.MStart}
				sg.a, sg.b, sg.ca, sg.cb = empty, empty, empty, empty
				return
			}
			last := idx == len(segs)-1
			sg.a = execute(buf, sg.base, sg.end, lexer.MStart, lim, "", 0, -1, false)
			sg.b = execute(buf, sg.base, sg.end, lexer.MQuoted, lim, "", 0, -1, false)
			if last {
				sg.ca = execute(buf, sg.base, sg.end, lexer.MStart, lim, "", 0, -1, true)
				sg.cb = execute(buf, sg.base, sg.end, lexer.MQuoted, lim, "", 0, -1, true)
			}
		}()
	}
	for range segs {
		<-done
	}

	// 阶段二：从左到右确定真实起点模式并拼接；特殊起点（悬空引号/CR/裸字段中段）
	// 即时按真实模式续跑一次（仅处理本段字节，长引号字段不重扫）。
	var all []ev
	var total int64
	mode, prefix := lexer.MStart, ""
	var flen, fstartIn int64
	carry := false
	for i, sg := range segs {
		var r *run
		switch mode {
		case lexer.MStart:
			r = sg.a
		case lexer.MQuoted:
			r = sg.b
		default:
			r = execute(buf, sg.base, sg.end, mode, lim, prefix, flen, fstartIn, false)
		}
		if i == len(segs)-1 {
			switch mode {
			case lexer.MStart:
				r = sg.ca
			case lexer.MQuoted:
				r = sg.cb
			default:
				r = execute(buf, sg.base, sg.end, mode, lim, prefix, flen, fstartIn, true)
			}
		}
		total += r.bytes
		if r.fatal != nil {
			return assemble(all, carry, r.fatal, total)
		}
		all = append(all, r.col.evs...)
		mode = r.mode
		prefix, flen, fstartIn = r.pref, r.flen, r.fstart
		carry = mode != lexer.MStart
	}
	return assemble(all, carry, nil, total)
}

// assemble 重放拼接事件：合并跨段字段、重建记录、列数校验，并换算全局错误坐标。
func assemble(all []ev, waitHead bool, fatal *lexer.Error, total int64) Result {
	var rows [][]cell.Cell
	var cur []cell.Cell
	var head *cell.Cell
	done, fcount := 0, 0
	res := Result{BytesSeen: total}
	appendCell := func(c cell.Cell) {
		if waitHead {
			cc := c
			head = &cc
			waitHead = false
			return
		}
		if head != nil {
			c.Value = head.Value + c.Value
			c.Start = head.Start
			head = nil
		}
		cur = append(cur, c)
		fcount++
	}
	for _, e := range all {
		if e.isRec {
			waitHead = false
			if head != nil {
				cur = append(cur, *head)
				fcount++
				head = nil
			}
			rows = append(rows, cur)
			cur = nil
			done++
			fcount = 0
			continue
		}
		appendCell(e.c)
	}
	if len(rows) > 0 {
		t := &table.Table{Header: rows[0]}
		for _, r := range rows[1:] {
			if len(r) != len(rows[0]) {
				res.Err = &table.Error{Kind: table.ErrColumnCount, Record: len(t.Rows) + 2, Field: len(r)}
				res.Table = t
				return res
			}
			t.Rows = append(t.Rows, r)
		}
		res.Table = t
	}
	if fatal != nil {
		res.Err = &table.Error{Kind: fatal.Kind, Offset: fatal.Offset, Record: done + 1, Field: fcount + 1}
	}
	return res
}
